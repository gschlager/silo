package cli

import (
	"fmt"
	"os"
	"strings"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
)

// shellQuote quotes each argument for safe use in a POSIX shell command string.
// Each argument is wrapped in single quotes, with any embedded single quotes
// escaped as '\'' (end quote, escaped quote, start quote).
func shellQuote(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}

// loadConfig loads and merges the global and project configuration.
func loadConfig() (*config.MergedConfig, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	global, err := config.LoadGlobalConfig()
	if err != nil {
		return nil, err
	}

	project, err := config.LoadProjectConfig(cwd)
	if err != nil {
		return nil, err
	}

	return config.Merge(global, project, cwd), nil
}

// requireRunning checks that the container exists and is running.
func requireRunning(server incuscli.InstanceServer, name string) error {
	if !incus.Exists(server, name) {
		return fmt.Errorf("container %s does not exist (run 'silo up' first)", name)
	}
	if !incus.IsRunning(server, name) {
		return fmt.Errorf("container %s is not running (run 'silo up' first)", name)
	}
	return nil
}
