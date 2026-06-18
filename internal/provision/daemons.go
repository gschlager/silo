package provision

import (
	"context"
	"fmt"
	"os"
	"sort"
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

		// Reload systemd user daemon. Units are installed but deliberately not
		// `enable`d: silo starts autostart daemons itself via StartConfiguredDaemons
		// (on first provision and every `silo up`) rather than letting systemd
		// autostart them at boot. That keeps silo in the loop at start time so it
		// can inject the resolved env (config env: plus secrets) into the user
		// manager first, and avoids a boot-then-restart cycle.
		if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
			"su", "-", username, "-c", "systemctl --user daemon-reload",
		}); err != nil {
			return fmt.Errorf("reloading systemd for daemon %q: %w", name, err)
		}
	}

	return nil
}

// daemonEnv resolves the environment daemons should run with: the project's
// configured env: vars plus resolved secrets (central secrets file, tool
// credentials, git credential). Secrets are resolved on the host; they are
// injected into the systemd user manager and never written to disk.
func daemonEnv(cfg *config.MergedConfig) (map[string]string, error) {
	env := make(map[string]string, len(cfg.Env))
	for k, v := range cfg.Env {
		env[k] = v
	}
	creds, err := ResolveSessionEnv(cfg)
	if err != nil {
		return nil, err
	}
	for k, v := range creds {
		env[k] = v
	}
	return env, nil
}

// injectDaemonEnv pushes the resolved daemon environment into the systemd user
// manager via `import-environment`, so units started or restarted afterwards
// inherit it. Values are passed through the shell `export` builtin and stdin,
// never as command arguments, so they don't show up in the process list, and
// they live only in the manager's memory — never on disk. Re-running it refreshes
// the values, which is how config and secret edits take effect on `silo up`
// without recreating the container.
func injectDaemonEnv(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig) error {
	env, err := daemonEnv(cfg)
	if err != nil {
		return err
	}
	if len(env) == 0 {
		return nil
	}

	if _, err := incus.ExecWithStdin(ctx, server, cfg.ContainerName, incus.ExecOpts{},
		[]string{"su", "-", cfg.User, "-c", "sh -s"}, []byte(envInjectionScript(env))); err != nil {
		return fmt.Errorf("injecting daemon environment: %w", err)
	}
	return nil
}

// envInjectionScript builds the POSIX-sh snippet fed to the user manager: each
// variable is set with the `export` builtin (so its value never appears in any
// process's argv) and then handed to `systemctl --user import-environment` by
// name only. Values are single-quote escaped, except PATH, which is a prepend
// that must expand $PATH — single-quoting it would set PATH to a literal that
// drops the system dirs, breaking the `systemctl` call on the next line.
func envInjectionScript(env map[string]string) string {
	var script strings.Builder
	names := make([]string, 0, len(env))
	for k, v := range env {
		if k == "PATH" {
			fmt.Fprintf(&script, "%s\n", pathPrependLine(v))
		} else {
			fmt.Fprintf(&script, "export %s=%s\n", k, shellEscape(v))
		}
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Fprintf(&script, "systemctl --user import-environment %s\n", strings.Join(names, " "))
	return script.String()
}

// StartConfiguredDaemons injects the resolved environment and starts every daemon
// marked autostart. Units are installed but not enabled for boot, so this is what
// actually starts them — on first provision and on every `silo up` — which also
// refreshes env: vars and secrets without recreating the container.
func StartConfiguredDaemons(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig) error {
	var names []string
	for name, daemon := range cfg.Daemons {
		if daemon.Autostart {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}

	if err := injectDaemonEnv(ctx, server, cfg); err != nil {
		return err
	}
	for _, name := range names {
		startDaemonUnit(ctx, server, cfg.ContainerName, cfg.User, name)
	}
	return nil
}

// ControlDaemon performs a start/stop/restart on a single daemon. start and
// restart first inject the resolved environment into the user manager so the
// daemon picks up env: vars and secrets; stop needs no environment.
func ControlDaemon(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig, action, name string) error {
	if action != "stop" {
		if err := injectDaemonEnv(ctx, server, cfg); err != nil {
			return err
		}
	}
	_, err := incus.Exec(ctx, server, cfg.ContainerName, incus.ExecOpts{}, []string{
		"su", "-", cfg.User, "-c",
		fmt.Sprintf("systemctl --user %s silo-%s.service", action, name),
	})
	return err
}

// startDaemonUnit starts a single daemon unit, surfacing the last journal lines
// if it fails to start (most often the daemon's own command exiting non-zero).
func startDaemonUnit(ctx context.Context, server incuscli.InstanceServer, container, username, name string) {
	serviceName := "silo-" + name
	if _, err := incus.Exec(ctx, server, container, incus.ExecOpts{}, []string{
		"su", "-", username, "-c",
		fmt.Sprintf("systemctl --user start %s.service", serviceName),
	}); err != nil {
		color.Warn("daemon %q failed to start:", name)
		tail, _ := incus.Exec(ctx, server, container, incus.ExecOpts{}, []string{
			"su", "-", username, "-c",
			fmt.Sprintf("journalctl --user -u %s.service -n 10 --no-pager 2>/dev/null || true", serviceName),
		})
		if tail != "" {
			fmt.Fprintln(os.Stderr, strings.TrimRight(tail, "\n"))
		}
	}
}

func buildUnitFile(name, shell, workspacePath string, daemon config.DaemonConfig) string {
	unit := fmt.Sprintf("[Unit]\nDescription=silo daemon: %s\n", name)
	if daemon.After != "" {
		dep := "silo-" + daemon.After + ".service"
		unit += fmt.Sprintf("After=%s\nRequires=%s\n", dep, dep)
	}
	// Run via login shell so the shell-neutral activation file (~/.silo/env.sh,
	// sourced via /etc/profile.d/silo.sh or ~/.zshenv) is loaded — this gives
	// daemons the same PATH and tool activations (rv, node version managers,
	// etc.) as interactive silo sessions, without per-daemon env.
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
