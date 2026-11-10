package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func sshStr(s []byte) []byte {
	b := make([]byte, 4, 4+len(s))
	binary.BigEndian.PutUint32(b, uint32(len(s)))
	return append(b, s...)
}

func sshsigBlob(namespace, hashAlg string, hash []byte) []byte {
	b := []byte("SSHSIG")
	b = append(b, sshStr([]byte(namespace))...)
	b = append(b, sshStr(nil)...)
	b = append(b, sshStr([]byte(hashAlg))...)
	return append(b, sshStr(hash)...)
}

func TestIsGitSSHSig(t *testing.T) {
	h := sha512.Sum512([]byte("tree 123\n"))
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"git sha512", sshsigBlob("git", "sha512", h[:]), true},
		{"git sha256", sshsigBlob("git", "sha256", h[:32]), true},
		{"other namespace", sshsigBlob("file", "sha512", h[:]), false},
		{"wrong hash length", sshsigBlob("git", "sha512", h[:32]), false},
		{"unknown hash", sshsigBlob("git", "md5", h[:16]), false},
		{"trailing data", append(sshsigBlob("git", "sha512", h[:]), 0), false},
		{"truncated", sshsigBlob("git", "sha512", h[:])[:20], false},
		// What an SSH client signs to log in starts with the session ID.
		{"ssh login", append(sshStr(make([]byte, 32)), 50), false},
		{"empty", nil, false},
	}
	for _, tt := range tests {
		if got := isGitSSHSig(tt.data); got != tt.want {
			t.Errorf("%s: isGitSSHSig = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// testKeys returns a host agent keyring with two keys; the first is the
// configured signing key.
func testKeys(t *testing.T) (agent.Agent, ssh.PublicKey, ssh.PublicKey) {
	t.Helper()
	ring := agent.NewKeyring()
	var pubs []ssh.PublicKey
	for i := 0; i < 2; i++ {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := ring.Add(agent.AddedKey{PrivateKey: priv, Comment: "key"}); err != nil {
			t.Fatal(err)
		}
		signer, _ := ssh.NewSignerFromKey(priv)
		pubs = append(pubs, signer.PublicKey())
	}
	return ring, pubs[0], pubs[1]
}

func newTestSigningAgent(ring agent.Agent, key ssh.PublicKey) *signingAgent {
	return &signingAgent{
		container:  "silo-demo",
		signingKey: func() (ssh.PublicKey, error) { return key, nil },
		upstream: func() (agent.ExtendedAgent, func(), error) {
			return ring.(agent.ExtendedAgent), func() {}, nil
		},
	}
}

func TestSigningAgentFilters(t *testing.T) {
	ring, signing, other := testKeys(t)
	a := newTestSigningAgent(ring, signing)

	keys, err := a.List()
	if err != nil || len(keys) != 1 || string(keys[0].Marshal()) != string(signing.Marshal()) {
		t.Fatalf("List = %v, %v; want only the signing key", keys, err)
	}

	h := sha512.Sum512([]byte("commit"))
	blob := sshsigBlob("git", "sha512", h[:])
	sig, err := a.Sign(signing, blob)
	if err != nil {
		t.Fatalf("signing a git blob: %v", err)
	}
	if err := signing.Verify(blob, sig); err != nil {
		t.Errorf("signature doesn't verify: %v", err)
	}

	if _, err := a.Sign(signing, append(sshStr(make([]byte, 32)), 50)); err == nil {
		t.Error("signed a login challenge")
	}
	if _, err := a.Sign(signing, sshsigBlob("file", "sha512", h[:])); err == nil {
		t.Error("signed for another namespace")
	}
	if _, err := a.Sign(other, blob); err == nil {
		t.Error("signed with a key that isn't the configured one")
	}
	if err := a.RemoveAll(); err == nil {
		t.Error("RemoveAll was allowed")
	}
	if _, err := a.Signers(); err == nil {
		t.Error("Signers was allowed")
	}

	none := newTestSigningAgent(ring, nil)
	if keys, _ := none.List(); len(keys) != 0 {
		t.Errorf("without a configured key List = %v", keys)
	}
}

// TestSigningWithSSHKeygen signs through the socket with ssh-keygen, the way
// git does with gpg.format=ssh.
func TestSigningWithSSHKeygen(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	ring, signing, _ := testKeys(t)
	dir, err := os.MkdirTemp("", "silo-sign")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	sock := filepath.Join(dir, "agent.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go serveSigningAgent(l, newTestSigningAgent(ring, signing))

	pubFile := filepath.Join(dir, "key.pub")
	os.WriteFile(pubFile, ssh.MarshalAuthorizedKey(signing), 0600)
	msg := filepath.Join(dir, "commit")
	os.WriteFile(msg, []byte("tree abc\n\nmessage\n"), 0600)

	sign := func(namespace string) error {
		cmd := exec.Command("ssh-keygen", "-Y", "sign", "-n", namespace, "-f", pubFile, msg)
		cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+sock)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return &exec.ExitError{Stderr: out}
		}
		return nil
	}
	if err := sign("git"); err != nil {
		t.Fatalf("ssh-keygen -Y sign -n git failed: %s", err.(*exec.ExitError).Stderr)
	}
	sigFile := msg + ".sig"
	check := exec.Command("ssh-keygen", "-Y", "check-novalidate", "-n", "git", "-s", sigFile)
	check.Stdin, _ = os.Open(msg)
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("signature doesn't verify: %s", out)
	}
	os.Remove(sigFile)

	if err := sign("file"); err == nil {
		t.Error("ssh-keygen signed for the \"file\" namespace")
	}

	// git with a literal key, the way silo configures containers.
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	repo := filepath.Join(dir, "repo")
	wrapper := filepath.Join(dir, "silo-ssh-sign")
	os.WriteFile(wrapper, []byte("#!/bin/sh\nSSH_AUTH_SOCK="+sock+" exec ssh-keygen \"$@\"\n"), 0755)
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "SSH_AUTH_SOCK=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	os.MkdirAll(repo, 0700)
	git("init", "-q")
	git("config", "user.name", "Dev")
	git("config", "user.email", "dev@example.com")
	git("config", "gpg.format", "ssh")
	git("config", "user.signingkey", "key::"+strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signing))))
	git("config", "gpg.ssh.program", wrapper)
	git("commit", "-q", "--allow-empty", "-S", "-m", "signed")
	if out := git("cat-file", "commit", "HEAD"); !strings.Contains(out, "-----BEGIN SSH SIGNATURE-----") {
		t.Errorf("commit isn't signed:\n%s", out)
	}
}
