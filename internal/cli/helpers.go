package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	incuscli "github.com/lxc/incus/v7/client"
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
// Each argument is wrapped in single quotes, with embedded quotes represented
// using the standard POSIX shell quote-break sequence.
func shellQuote(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}

// loadConfig loads the configuration for the project in the current
// directory (see provision.LoadConfig).
func loadConfig() (*config.MergedConfig, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}
	return provision.LoadConfig(cwd)
}

// sessionEnv returns environment variables for an interactive container session:
// host terminal env (PassEnv) plus the project's secrets from secrets.yml.
func sessionEnv(cfg *config.MergedConfig) (map[string]string, error) {
	env := cfg.HostEnv()
	creds, err := provision.ResolveSessionEnv(cfg)
	if err != nil {
		return nil, err
	}
	for k, v := range creds {
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

// writeAgentFunctions refreshes the agents' shell functions in the container
// (see provision.WriteAgentFunctions), so cmd: edits apply without a recreate.
func writeAgentFunctions(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig) {
	if err := provision.WriteAgentFunctions(ctx, server, cfg.ContainerName, cfg.User, cfg.Agents); err != nil {
		color.Warn("could not write agent functions: %v", err)
	}
}
