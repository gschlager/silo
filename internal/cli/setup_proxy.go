package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/localca"
	"github.com/gschlager/silo/internal/svcapi"
)

// ensureBindCapability gives the silo binary cap_net_bind_service, so the
// silo service (a user service) can run the .localhost proxy on ports 80 and
// 443. Only that one capability: silo can bind low ports and nothing else.
// The RPM sets it already; a rebuilt binary loses it.
func ensureBindCapability() {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		color.Warn("  can't find the silo binary: %v", err)
		return
	}
	if out, err := exec.Command("getcap", exe).Output(); err == nil && strings.Contains(string(out), "cap_net_bind_service") {
		color.Info("  %s can already use ports 80 and 443.", exe)
		return
	}
	if err := runSudo("", "setcap", "cap_net_bind_service=ep", exe); err != nil {
		color.Warn("  couldn't set the capability (%v); the proxy uses ports 7080 and 7443 instead.", err)
	}
}

const (
	caName       = "silo localhost CA"
	caAnchorName = "silo-localhost-ca.crt"
)

// ensureCATrusted creates silo's local CA (if needed) and adds it to the
// system trust store and to the NSS databases Chrome and Firefox use, so
// https://<project>.localhost works without warnings. The CA can only issue
// certificates for names under .localhost (see localca).
func ensureCATrusted() {
	if _, err := localca.Ensure(); err != nil {
		color.Warn("  can't create the local CA: %v", err)
		return
	}
	cert, err := os.ReadFile(localca.CertPath())
	if err != nil {
		color.Warn("  %v", err)
		return
	}

	switch {
	case dirExists("/etc/pki/ca-trust/source/anchors"): // Fedora, RHEL
		trustSystemCA(cert, "/etc/pki/ca-trust/source/anchors/"+caAnchorName, "update-ca-trust")
	case dirExists("/usr/local/share/ca-certificates"): // Debian, Ubuntu
		trustSystemCA(cert, "/usr/local/share/ca-certificates/"+caAnchorName, "update-ca-certificates")
	default:
		color.Warn("  don't know this system's trust store; add %s to it yourself.", localca.CertPath())
	}

	if _, err := exec.LookPath("certutil"); err != nil {
		color.Info("  certutil isn't installed (nss-tools), so Chrome and Firefox only see the CA if they use the system trust store.")
		return
	}
	home, _ := os.UserHomeDir()
	dbs := []string{filepath.Join(home, ".pki", "nssdb")} // Chrome, Chromium
	for _, pattern := range []string{
		filepath.Join(home, ".mozilla", "firefox", "*"),
		filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox", "*"),
		filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox", "*"),
	} {
		profiles, _ := filepath.Glob(pattern)
		for _, p := range profiles {
			if fileExists(filepath.Join(p, "cert9.db")) {
				dbs = append(dbs, p)
			}
		}
	}
	for _, db := range dbs {
		if err := trustNSS(db); err != nil {
			color.Warn("  couldn't add the CA to %s: %v", db, err)
		}
	}
	color.Info("  The CA is trusted (restart your browser if https://<project>.localhost still warns).")
}

func trustSystemCA(cert []byte, anchor, update string) {
	if cur, err := os.ReadFile(anchor); err == nil && bytes.Equal(cur, cert) {
		return
	}
	if err := runSudo("", "install", "-m", "0644", localca.CertPath(), anchor); err != nil {
		color.Warn("  couldn't add the CA to the system trust store: %v", err)
		return
	}
	if err := runSudo("", update); err != nil {
		color.Warn("  %s failed: %v", update, err)
	}
}

// trustNSS adds the CA to an NSS database, replacing an older silo CA.
func trustNSS(dir string) error {
	db := "sql:" + dir
	if !fileExists(filepath.Join(dir, "cert9.db")) {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if out, err := exec.Command("certutil", "-N", "-d", db, "--empty-password").CombinedOutput(); err != nil {
			return errorWithOutput(err, out)
		}
	}
	exec.Command("certutil", "-D", "-d", db, "-n", caName).Run()
	if out, err := exec.Command("certutil", "-A", "-d", db, "-t", "C,,", "-n", caName, "-i", localca.CertPath()).CombinedOutput(); err != nil {
		return errorWithOutput(err, out)
	}
	return nil
}

// restartServiceForProxyPorts restarts the silo service when its proxy runs
// on the fallback ports although silo may now bind 80 and 443.
func restartServiceForProxyPorts() {
	c, err := svcapi.Dial()
	if err != nil {
		return
	}
	info, err := c.Proxy()
	if err != nil || info.HTTPPort == 80 {
		return
	}
	color.Info("  Restarting the silo service so its proxy can use ports 80 and 443.")
	cmd := newServiceRestartCmd()
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		color.Warn("  %v", err)
	}
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func errorWithOutput(err error, out []byte) error {
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return &outputError{err: err, out: msg}
	}
	return err
}

type outputError struct {
	err error
	out string
}

func (e *outputError) Error() string { return e.err.Error() + ": " + e.out }
