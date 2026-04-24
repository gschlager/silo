package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	"github.com/spf13/cobra"
)

// requireArgs returns a cobra.PositionalArgs validator that prints
// a friendly error with usage hint instead of cobra's default message.
func requireArgs(n int, what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < n {
			return fmt.Errorf("missing %s\n\n%s", what, cmd.UsageString())
		}
		return nil
	}
}

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

// loadConfig loads and merges the global and project configuration,
// then applies any per-container mode overrides from silo mode state.
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

	merged := config.Merge(global, project, cwd)

	// Apply mode overrides from state file.
	modes, err := config.LoadModeState(merged.ContainerName)
	if err != nil {
		return nil, err
	}
	for agentName, mode := range modes {
		if agent, ok := merged.Agents[agentName]; ok {
			agent.Mode = mode
			merged.Agents[agentName] = agent
		}
	}

	return merged, nil
}

// sessionEnv returns environment variables for an interactive container session:
// host terminal env (PassEnv), tool credentials, and project env.
func sessionEnv(cfg *config.MergedConfig) (map[string]string, error) {
	env := cfg.HostEnv()
	tools, err := provision.ResolveToolEnv(cfg.Tools)
	if err != nil {
		return nil, err
	}
	for k, v := range tools {
		env[k] = v
	}
	return env, nil
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

// ensureRunning makes sure the container exists and is running, lazy-starting
// it if stopped.
func ensureRunning(ctx context.Context, server incuscli.InstanceServer, name string) error {
	if !incus.Exists(server, name) {
		return fmt.Errorf("container %s does not exist (run 'silo up' first)", name)
	}
	if !incus.IsRunning(server, name) {
		color.Status("Starting %s...", name)
		if err := incus.Start(ctx, server, name); err != nil {
			return err
		}
	}
	return nil
}
