package provision

import (
	"context"
	"fmt"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
)

// SetupDaemons generates systemd user service units inside the container.
func SetupDaemons(ctx context.Context, server incuscli.InstanceServer, container, username, workspacePath string, daemons map[string]config.DaemonConfig) error {
	if len(daemons) == 0 {
		return nil
	}

	rootOpts := incus.ExecOpts{}

	// Ensure the systemd user directory exists.
	unitDir := fmt.Sprintf("/home/%s/.config/systemd/user", username)
	if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
		"su", "-", username, "-c", fmt.Sprintf("mkdir -p %s", unitDir),
	}); err != nil {
		return fmt.Errorf("creating systemd user directory: %w", err)
	}

	for name, daemon := range daemons {
		serviceName := "silo-" + name
		unitContent := buildUnitFile(name, workspacePath, daemon)

		// Write the unit file.
		unitPath := fmt.Sprintf("%s/%s.service", unitDir, serviceName)
		if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
			"sh", "-c", fmt.Sprintf(
				`cat > %s << 'EOF'
%sEOF
chown %s:%s %s`, unitPath, unitContent, username, username, unitPath),
		}); err != nil {
			return fmt.Errorf("writing unit file for daemon %q: %w", name, err)
		}

		// Reload systemd user daemon.
		if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
			"su", "-", username, "-c", "systemctl --user daemon-reload",
		}); err != nil {
			return fmt.Errorf("reloading systemd for daemon %q: %w", name, err)
		}

		// Enable autostart daemons.
		if daemon.Autostart {
			if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
				"su", "-", username, "-c",
				fmt.Sprintf("systemctl --user enable %s.service", serviceName),
			}); err != nil {
				return fmt.Errorf("enabling daemon %q: %w", name, err)
			}
		}
	}

	return nil
}

func buildUnitFile(name, workspacePath string, daemon config.DaemonConfig) string {
	unit := fmt.Sprintf("[Unit]\nDescription=silo daemon: %s\n", name)
	if daemon.After != "" {
		dep := "silo-" + daemon.After + ".service"
		unit += fmt.Sprintf("After=%s\nRequires=%s\n", dep, dep)
	}
	unit += fmt.Sprintf(`
[Service]
Type=simple
WorkingDirectory=%s
ExecStart=/bin/sh -c '%s'
Restart=no
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`, workspacePath, daemon.Cmd)
	return unit
}
