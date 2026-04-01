package provision

import (
	"fmt"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
)

const serviceTemplate = `[Unit]
Description=silo daemon: %s

[Service]
Type=simple
WorkingDirectory=/workspace
ExecStart=/bin/sh -c '%s'
Restart=no
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`

// SetupDaemons generates systemd user service units inside the container.
func SetupDaemons(server incuscli.InstanceServer, container, username string, daemons map[string]config.DaemonConfig) error {
	if len(daemons) == 0 {
		return nil
	}

	rootOpts := incus.ExecOpts{}

	// Ensure the systemd user directory exists.
	unitDir := fmt.Sprintf("/home/%s/.config/systemd/user", username)
	if _, err := incus.Exec(server, container, rootOpts, []string{
		"su", "-", username, "-c", fmt.Sprintf("mkdir -p %s", unitDir),
	}); err != nil {
		return fmt.Errorf("creating systemd user directory: %w", err)
	}

	for name, daemon := range daemons {
		serviceName := "silo-" + name
		unitContent := fmt.Sprintf(serviceTemplate, name, daemon.Cmd)

		// Write the unit file.
		unitPath := fmt.Sprintf("%s/%s.service", unitDir, serviceName)
		if _, err := incus.Exec(server, container, rootOpts, []string{
			"sh", "-c", fmt.Sprintf(
				`cat > %s << 'EOF'
%sEOF
chown %s:%s %s`, unitPath, unitContent, username, username, unitPath),
		}); err != nil {
			return fmt.Errorf("writing unit file for daemon %q: %w", name, err)
		}

		// Reload systemd user daemon.
		if _, err := incus.Exec(server, container, rootOpts, []string{
			"su", "-", username, "-c", "systemctl --user daemon-reload",
		}); err != nil {
			return fmt.Errorf("reloading systemd for daemon %q: %w", name, err)
		}

		// Enable autostart daemons.
		if daemon.Autostart {
			if _, err := incus.Exec(server, container, rootOpts, []string{
				"su", "-", username, "-c",
				fmt.Sprintf("systemctl --user enable %s.service", serviceName),
			}); err != nil {
				return fmt.Errorf("enabling daemon %q: %w", name, err)
			}
		}
	}

	return nil
}
