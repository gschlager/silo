// Package localca is silo's local certificate authority for HTTPS on
// <project>.localhost. No public CA issues certificates for .localhost, so
// silo makes its own and `silo setup` adds it to the system trust store.
//
// The CA is name-constrained: it can only issue certificates for localhost
// and names under it (and loopback IPs), and only for TLS servers. Browsers
// and the system verifier enforce that, so even if its key leaked, it
// couldn't be used to impersonate any other site.
package localca

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gschlager/silo/internal/config"
)

const (
	caValidity   = 10 * 365 * 24 * time.Hour
	leafValidity = 30 * 24 * time.Hour
	// leafRenewBefore is how long before expiry a cached leaf is replaced.
	leafRenewBefore = 7 * 24 * time.Hour
	// maxLeaves bounds the certificates kept in memory.
	maxLeaves = 256
)

// Dir is where the CA lives: ~/.config/silo/ca.
func Dir() string {
	return filepath.Join(config.GlobalConfigDir(), "ca")
}

// CertPath is the CA certificate (public, this is what gets trusted).
func CertPath() string { return filepath.Join(Dir(), "silo-localhost-ca.crt") }

func keyPath() string { return filepath.Join(Dir(), "silo-localhost-ca.key") }

// CA is a loaded certificate authority.
type CA struct {
	Cert *x509.Certificate
	key  crypto.Signer

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// Ensure loads the CA, creating it on first use.
func Ensure() (*CA, error) {
	if ca, err := load(); err == nil {
		return ca, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := create(); err != nil {
		return nil, err
	}
	return load()
}

func load() (*CA, error) {
	certPEM, err := os.ReadFile(CertPath())
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath())
	if err != nil {
		return nil, err
	}
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, fmt.Errorf("can't read the CA in %s", Dir())
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

func create() error {
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject: pkix.Name{
			Organization: []string{"silo"},
			CommonName:   "silo localhost CA (" + host + ")",
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},

		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{"localhost"},
		PermittedIPRanges: []*net.IPNet{
			{IP: net.IPv4(127, 0, 0, 0).To4(), Mask: net.CIDRMask(8, 32)},
			{IP: net.IPv6loopback, Mask: net.CIDRMask(128, 128)},
		},
		PermittedEmailAddresses: []string{"localhost"},
		PermittedURIDomains:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath(), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		return err
	}
	return os.WriteFile(CertPath(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644)
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

// Allowed reports whether the CA may issue a certificate for host.
func Allowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

// GetCertificate returns a certificate for the SNI name, made on first use
// and kept in memory. It's meant for tls.Config.GetCertificate.
func (ca *CA) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	if name == "" {
		name = "localhost"
	}
	if !Allowed(name) {
		return nil, fmt.Errorf("silo only serves names under .localhost, not %q", name)
	}
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if c, ok := ca.leaves[name]; ok && time.Until(c.Leaf.NotAfter) > leafRenewBefore {
		return c, nil
	}
	c, err := ca.issue(name)
	if err != nil {
		return nil, err
	}
	if len(ca.leaves) >= maxLeaves {
		clear(ca.leaves) // any name under .localhost works, so keep this bounded
	}
	ca.leaves[name] = c
	return c, nil
}

func (ca *CA) issue(name string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der, ca.Cert.Raw}, PrivateKey: key, Leaf: leaf}, nil
}
