package service

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/gschlager/silo/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// The signing proxy: each container gets an SSH agent socket that forwards to
// the host's agent (1Password's, normally), but only:
//
//   - lists the signing key from the global git config, no other key
//   - signs data in the SSHSIG format with the namespace "git", which is what
//     `ssh-keygen -Y sign -n git` (and so `git commit -S`) asks for. An SSH
//     login challenge can't look like that (OpenSSH added the SSHSIG header
//     for exactly this), so the key can't be used to log in anywhere.
//   - refuses everything else: adding or removing keys, locking, extensions
//
// The host agent sees the silo service as the application, so 1Password asks
// once per unlock for silo, not per container.

var errRefused = errors.New("refused by silo: this agent only signs git commits with the configured key")

// signingAgent is the agent.ExtendedAgent a container's socket serves.
type signingAgent struct {
	container string
	// signingKey returns the configured key; read on every request, so config
	// edits apply right away.
	signingKey func() (ssh.PublicKey, error)
	// upstream connects to the host's agent.
	upstream func() (agent.ExtendedAgent, func(), error)
}

func (a *signingAgent) List() ([]*agent.Key, error) {
	want, err := a.signingKey()
	if err != nil || want == nil {
		return nil, nil
	}
	up, done, err := a.upstream()
	if err != nil {
		log.Printf("%s: signing: %v", a.container, err)
		return nil, nil
	}
	defer done()
	keys, err := up.List()
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if bytes.Equal(k.Marshal(), want.Marshal()) {
			return []*agent.Key{k}, nil
		}
	}
	log.Printf("%s: signing: the host's SSH agent doesn't have the configured key", a.container)
	return nil, nil
}

func (a *signingAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	return a.SignWithFlags(key, data, 0)
}

func (a *signingAgent) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	want, err := a.signingKey()
	if err != nil || want == nil || !bytes.Equal(key.Marshal(), want.Marshal()) {
		log.Printf("%s: signing: refused a request for another key", a.container)
		return nil, errRefused
	}
	if !isGitSSHSig(data) {
		log.Printf("%s: signing: refused data that isn't a git signature", a.container)
		return nil, errRefused
	}
	up, done, err := a.upstream()
	if err != nil {
		return nil, err
	}
	defer done()
	sig, err := up.SignWithFlags(key, data, flags)
	if err != nil {
		log.Printf("%s: signing: the host's agent didn't sign: %v", a.container, err)
		return nil, err
	}
	log.Printf("%s: signed a git object", a.container)
	return sig, nil
}

func (a *signingAgent) Add(agent.AddedKey) error       { return errRefused }
func (a *signingAgent) Remove(ssh.PublicKey) error     { return errRefused }
func (a *signingAgent) RemoveAll() error               { return errRefused }
func (a *signingAgent) Lock([]byte) error              { return errRefused }
func (a *signingAgent) Unlock([]byte) error            { return errRefused }
func (a *signingAgent) Signers() ([]ssh.Signer, error) { return nil, errRefused }
func (a *signingAgent) Extension(string, []byte) ([]byte, error) {
	return nil, agent.ErrExtensionUnsupported
}

// isGitSSHSig reports whether data is what ssh-keygen signs for an SSHSIG
// signature in the "git" namespace (see OpenSSH's PROTOCOL.sshsig):
//
//	byte[6]  "SSHSIG"
//	string   namespace ("git")
//	string   reserved
//	string   hash_algorithm ("sha256" or "sha512")
//	string   H(message)
func isGitSSHSig(data []byte) bool {
	rest, ok := bytes.CutPrefix(data, []byte("SSHSIG"))
	if !ok {
		return false
	}
	var fields [4][]byte
	for i := range fields {
		if fields[i], rest, ok = sshString(rest); !ok {
			return false
		}
	}
	namespace, hashAlg, hash := string(fields[0]), string(fields[2]), fields[3]
	if namespace != "git" || len(rest) != 0 {
		return false
	}
	switch hashAlg {
	case "sha256":
		return len(hash) == 32
	case "sha512":
		return len(hash) == 64
	}
	return false
}

func sshString(b []byte) (s, rest []byte, ok bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	n := binary.BigEndian.Uint32(b)
	if uint64(len(b)-4) < uint64(n) {
		return nil, nil, false
	}
	return b[4 : 4+n], b[4+n:], true
}

// configuredSigningKey parses the signing key from the global config.
func configuredSigningKey() (ssh.PublicKey, error) {
	g, err := config.LoadGlobalConfig()
	if err != nil {
		return nil, err
	}
	key := g.SigningKey()
	if key == "" {
		return nil, nil
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key))
	if err != nil {
		return nil, fmt.Errorf("parsing user.signingkey: %w", err)
	}
	return pub, nil
}

// hostAgentSocket is the host's SSH agent: 1Password's when it's there,
// otherwise whatever SSH_AUTH_SOCK the service got from the session.
func hostAgentSocket() string {
	home, _ := os.UserHomeDir()
	op := filepath.Join(home, ".1password", "agent.sock")
	if _, err := os.Stat(op); err == nil {
		return op
	}
	return os.Getenv("SSH_AUTH_SOCK")
}

// dialHostAgent connects to the host's SSH agent.
func dialHostAgent() (agent.ExtendedAgent, func(), error) {
	path := hostAgentSocket()
	if path == "" {
		return nil, nil, errors.New("no SSH agent on the host (no ~/.1password/agent.sock and no SSH_AUTH_SOCK)")
	}
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to the host's SSH agent: %w", err)
	}
	return agent.NewClient(conn), func() { conn.Close() }, nil
}

// serveSigningAgent answers agent requests on l until it's closed.
func serveSigningAgent(l net.Listener, a *signingAgent) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			// Signing waits for the 1Password approval, so allow some time.
			conn.SetDeadline(time.Now().Add(3 * time.Minute))
			agent.ServeAgent(a, conn)
		}()
	}
}
