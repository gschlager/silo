package cli

import (
	"context"
	"sort"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/provision"
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

// daemonStates maps each daemon to "running", "failed" or "stopped" (see
// provision.DaemonStates).
func daemonStates(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig, names []string) map[string]string {
	return provision.DaemonStates(ctx, server, cfg.ContainerName, cfg.User, names)
}
