package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/gschlager/silo/internal/agents"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newRaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ra <agent> [prompt or file]",
		Short: "Run an AI agent interactively inside the container",
		Long: `Run an AI agent interactively inside the container.
Copies agent-specific auth/config, then launches the agent with TTY attached.

Examples:
  silo ra claude
  silo ra claude "fix the failing tests"
  silo ra claude ./prompt.md`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			server, err := incus.Connect()
			if err != nil {
				return err
			}

			if err := requireRunning(server, cfg.ContainerName); err != nil {
				return err
			}

			agentName := args[0]
			agentCfg, ok := cfg.Agents[agentName]
			if !ok {
				return fmt.Errorf("unknown agent %q (configured agents: %s)", agentName, agentNames(cfg))
			}

			// Refresh "always" seed files.
			fmt.Fprintf(os.Stderr, "Refreshing %s credentials...\n", agentName)
			if err := agents.RefreshAlwaysSeeds(cfg.ContainerName, cfg.Agents); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not refresh seeds: %v\n", err)
			}

			// Build environment variables.
			env := make(map[string]string)
			for k, v := range agentCfg.Env {
				env[k] = v
			}

			// Build the agent command.
			agentCmd := []string{agentName}

			// Handle prompt argument.
			if len(args) > 1 {
				prompt := args[1]
				// Check if it's a file path on the host.
				if info, err := os.Stat(prompt); err == nil && !info.IsDir() {
					data, err := os.ReadFile(prompt)
					if err != nil {
						return fmt.Errorf("reading prompt file %q: %w", prompt, err)
					}
					prompt = string(data)
				}
				agentCmd = append(agentCmd, "-p", prompt)
			}

			// Clear screen and launch the agent.
			fmt.Print("\033[2J\033[H")
			shellCmd := "cd /workspace && " + strings.Join(agentCmd, " ")
			opts := incus.UserOpts(cfg.UserHome(), "/workspace")
			opts.Env = env
			return incus.ExecInteractive(server, cfg.ContainerName, opts, cfg.LoginCmd(shellCmd))
		},
	}
}

func agentNames(cfg *config.MergedConfig) string {
	var names []string
	for name := range cfg.Agents {
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}
