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

// ReconcileDaemons makes the container's installed systemd user units match the
// configured set of daemons: it (re)writes a unit for every configured daemon
// and removes any leftover silo-*.service units that are no longer configured.
// This runs on first provision and on every `silo up`, so switching branches —
// which can add or drop daemons — keeps the units in sync without recreating the
// container. It is idempotent.
func ReconcileDaemons(ctx context.Context, server incuscli.InstanceServer, container, username, shell, workspacePath string, daemons map[string]config.DaemonConfig) error {
	rootOpts := incus.ExecOpts{}

	// Ensure the systemd user directory exists.
	unitDir := fmt.Sprintf("/home/%s/.config/systemd/user", username)
	if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
		"su", "-", username, "-c", fmt.Sprintf("mkdir -p %s", unitDir),
	}); err != nil {
		return fmt.Errorf("creating systemd user directory: %w", err)
	}

	for name, daemon := range daemons {
		if err := writeDaemonUnit(ctx, server, container, username, unitDir, name, shell, workspacePath, daemon); err != nil {
			return err
		}
	}

	if err := pruneOrphanDaemons(ctx, server, container, username, unitDir, daemons); err != nil {
		return err
	}

	// Reload once so the user manager picks up every added and removed unit.
	// Units are installed but deliberately not `enable`d: silo starts autostart
	// daemons itself via StartConfiguredDaemons (on first provision and every
	// `silo up`) rather than letting systemd autostart them at boot. That keeps
	// silo in the loop at start time so it can inject the resolved env (config
	// env: plus secrets) into the user manager first, and avoids a
	// boot-then-restart cycle.
	if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
		"su", "-", username, "-c", "systemctl --user daemon-reload",
	}); err != nil {
		return fmt.Errorf("reloading systemd user manager: %w", err)
	}

	return nil
}

// pruneOrphanDaemons stops and removes any silo-*.service units in unitDir that
// are no longer in the configured daemon set, so a daemon dropped on a branch
// switch doesn't linger as a stale unit (and keep showing up in `daemon list`).
func pruneOrphanDaemons(ctx context.Context, server incuscli.InstanceServer, container, username, unitDir string, daemons map[string]config.DaemonConfig) error {
	rootOpts := incus.ExecOpts{}
	out, err := incus.Exec(ctx, server, container, rootOpts, []string{
		"su", "-", username, "-c",
		fmt.Sprintf("ls -1 %s/silo-*.service 2>/dev/null || true", unitDir),
	})
	if err != nil {
		return fmt.Errorf("listing daemon units: %w", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		base := line[strings.LastIndex(line, "/")+1:]
		name := strings.TrimSuffix(strings.TrimPrefix(base, "silo-"), ".service")
		if _, ok := daemons[name]; ok {
			continue
		}
		// Stop a possibly-running orphan, then remove its unit file. The single
		// daemon-reload back in ReconcileDaemons makes the manager forget it.
		if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
			"su", "-", username, "-c",
			fmt.Sprintf("systemctl --user stop silo-%s.service 2>/dev/null; rm -f %s", name, line),
		}); err != nil {
			return fmt.Errorf("removing orphaned daemon %q: %w", name, err)
		}
	}
	return nil
}

// writeDaemonUnit renders and installs the unit file for one daemon. It does not
// reload the user manager; callers batch a single daemon-reload after writing.
func writeDaemonUnit(ctx context.Context, server incuscli.InstanceServer, container, username, unitDir, name, shell, workspacePath string, daemon config.DaemonConfig) error {
	unitContent := buildUnitFile(name, shell, workspacePath, daemon)
	unitPath := fmt.Sprintf("%s/silo-%s.service", unitDir, name)
	if _, err := incus.Exec(ctx, server, container, incus.ExecOpts{}, []string{
		"sh", "-c", fmt.Sprintf(
			`cat > %s << 'EOF'
%sEOF
chown %s:%s %s`, unitPath, unitContent, username, username, unitPath),
	}); err != nil {
		return fmt.Errorf("writing unit file for daemon %q: %w", name, err)
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
