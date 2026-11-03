package cli

import (
	"context"
	"sort"
	"strings"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	incuscli "github.com/lxc/incus/v7/client"
)

// sortedDaemonNames returns the configured daemon names in alphabetical order.
func sortedDaemonNames(cfg *config.MergedConfig) []string {
	names := make([]string, 0, len(cfg.Daemons))
	for name := range cfg.Daemons {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// daemonStates asks systemd for the state of the given daemons in a single exec
// and maps each name to "running", "failed" or "stopped". The container must be
// running; on an exec error every daemon is reported as "stopped".
func daemonStates(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig, names []string) map[string]string {
	states := make(map[string]string, len(names))
	for _, name := range names {
		states[name] = "stopped"
	}
	if len(names) == 0 {
		return states
	}
	units := make([]string, len(names))
	for i, name := range names {
		units[i] = "silo-" + name
	}
	// is-active prints one line per unit, in argument order.
	out, err := incus.Exec(ctx, server, cfg.ContainerName, incus.ExecOpts{},
		incus.SuUserManager(cfg.User,
			"systemctl --user is-active "+strings.Join(units, " ")+" 2>/dev/null || true"))
	if err != nil {
		return states
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i, name := range names {
		if i >= len(lines) {
			break
		}
		switch strings.TrimSpace(lines[i]) {
		case "active":
			states[name] = "running"
		case "failed":
			states[name] = "failed"
		}
	}
	return states
}
