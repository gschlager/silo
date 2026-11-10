package localca

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"testing"
)

func TestCAIssuesOnlyLocalhost(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ca, err := Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(keyPath()); err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("CA key mode: %v %v", fi, err)
	}
	again, err := Ensure()
	if err != nil || !again.Cert.Equal(ca.Cert) {
		t.Fatal("Ensure made a new CA instead of loading the existing one")
	}

	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)

	c, err := ca.GetCertificate(&tls.ClientHelloInfo{ServerName: "myapp.localhost"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Leaf.Verify(x509.VerifyOptions{DNSName: "myapp.localhost", Roots: roots}); err != nil {
		t.Errorf("leaf for myapp.localhost doesn't verify: %v", err)
	}
	if c2, _ := ca.GetCertificate(&tls.ClientHelloInfo{ServerName: "myapp.localhost"}); c2 != c {
		t.Error("leaf wasn't cached")
	}

	if _, err := ca.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"}); err == nil {
		t.Error("issued a certificate for example.com")
	}

	// Even a certificate signed with the CA key for another name must not
	// verify: the name constraints are what protects you if the key leaks.
	forged, err := ca.issue("github.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := forged.Leaf.Verify(x509.VerifyOptions{DNSName: "github.com", Roots: roots}); err == nil {
		t.Error("a github.com certificate from the CA verified despite the name constraints")
	}
}

func TestAllowed(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost":          true,
		"myapp.localhost":    true,
		"MyApp.LOCALHOST.":   true,
		"localhost.evil.com": false,
		"evillocalhost":      false,
		"example.com":        false,
	} {
		if got := Allowed(host); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", host, got, want)
		}
	}
}
