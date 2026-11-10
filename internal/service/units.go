package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The systemd user units. The RPM ships the same files (packaging/silo.socket
// and packaging/silo.service, kept in sync by a test) for /usr/bin/silo;
// `silo setup` writes them to ~/.config/systemd/user for other installs.

const socketUnit = `[Unit]
Description=silo service socket

[Socket]
ListenStream=%t/silo/silo.sock
SocketMode=0600
DirectoryMode=0700

[Install]
WantedBy=sockets.target
`

const serviceUnitTemplate = `[Unit]
Description=silo service (secrets cache, container sockets, .localhost proxy)
Requires=silo.socket
After=silo.socket

[Service]
ExecStart=%s service run
Restart=on-failure

[Install]
WantedBy=default.target
Also=silo.socket
`

// ServiceUnit returns silo.service for the silo binary at exe.
func ServiceUnit(exe string) string {
	return fmt.Sprintf(serviceUnitTemplate, exe)
}

// SocketUnit returns silo.socket.
func SocketUnit() string { return socketUnit }

// systemUnitPath is where the RPM installs the user units.
const systemUnitPath = "/usr/lib/systemd/user/silo.service"

// InstallUnits makes sure the user units exist and are enabled and running.
// When the RPM installed them it only enables them; otherwise it writes them
// for the binary at exe (rewriting them when the binary moved). It returns a
// description of what it did.
func InstallUnits(exe string) (string, error) {
	var did []string
	if _, err := os.Stat(systemUnitPath); err != nil {
		dir := userUnitDir()
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", err
		}
		changed := false
		for name, content := range map[string]string{
			"silo.socket":  SocketUnit(),
			"silo.service": ServiceUnit(exe),
		} {
			path := filepath.Join(dir, name)
			if cur, err := os.ReadFile(path); err == nil && string(cur) == content {
				continue
			}
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return "", err
			}
			changed = true
		}
		if changed {
			did = append(did, "wrote the units to "+dir)
			if err := systemctl("daemon-reload"); err != nil {
				return "", err
			}
		}
	}
	if systemctl("is-enabled", "--quiet", "silo.socket") != nil || systemctl("is-enabled", "--quiet", "silo.service") != nil {
		if err := systemctl("enable", "silo.socket", "silo.service"); err != nil {
			return "", err
		}
		did = append(did, "enabled silo.socket and silo.service")
	}
	if systemctl("is-active", "--quiet", "silo.service") != nil {
		if err := systemctl("start", "silo.socket", "silo.service"); err != nil {
			return "", err
		}
		did = append(did, "started the service")
	}
	return strings.Join(did, ", "), nil
}

func userUnitDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "systemd", "user")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil && !strings.Contains(strings.Join(args, " "), "--quiet") {
		return fmt.Errorf("systemctl --user %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return err
}
