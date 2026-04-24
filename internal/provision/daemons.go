package provision

import (
	"context"
	"fmt"
	"os"
	"strings"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
)

// SetupDaemons generates systemd user service units inside the container.
func SetupDaemons(ctx context.Context, server incuscli.InstanceServer, container, username, shell, workspacePath string, daemons map[string]config.DaemonConfig) error {
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
		unitContent := buildUnitFile(name, shell, workspacePath, daemon)

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

		// Enable autostart daemons and start them immediately. `enable --now`
		// both registers for future boots and starts the unit in the current
		// session. If start fails (e.g. bad command), surface the failure
		// with the last journal lines so the user doesn't have to hunt for it.
		if daemon.Autostart {
			if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
				"su", "-", username, "-c",
				fmt.Sprintf("systemctl --user enable --now %s.service", serviceName),
			}); err != nil {
				color.Warn("daemon %q failed to start:", name)
				tail, _ := incus.Exec(ctx, server, container, rootOpts, []string{
					"su", "-", username, "-c",
					fmt.Sprintf("journalctl --user -u %s.service -n 10 --no-pager 2>/dev/null || true", serviceName),
				})
				if tail != "" {
					fmt.Fprintln(os.Stderr, strings.TrimRight(tail, "\n"))
				}
			}
		}
	}

	return nil
}

func buildUnitFile(name, shell, workspacePath string, daemon config.DaemonConfig) string {
	unit := fmt.Sprintf("[Unit]\nDescription=silo daemon: %s\n", name)
	if daemon.After != "" {
		dep := "silo-" + daemon.After + ".service"
		unit += fmt.Sprintf("After=%s\nRequires=%s\n", dep, dep)
	}
	// Run via login shell so the user's profile/shellenv is sourced — this
	// gives daemons the same PATH and tool activations (rv, node version
	// managers, etc.) as interactive silo sessions, without per-daemon env.
	unit += fmt.Sprintf(`
[Service]
Type=simple
WorkingDirectory=%s
ExecStart=/bin/%s -lc '%s'
Restart=no
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`, workspacePath, shell, daemon.Cmd)
	return unit
}
