package cli

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/gschlager/silo/internal/agents"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newRaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ra [agent] [prompt or file]",
		Short: "Run an AI agent interactively inside the container",
		Long: `Run an AI agent interactively inside the container.
Copies agent-specific auth/config, then launches the agent with TTY attached.
Without arguments, runs the default agent.

Examples:
  silo ra
  silo ra claude
  silo ra claude "fix the failing tests"
  silo ra claude ./prompt.md`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

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

			agentName := cfg.ResolveDefaultAgent()
			promptArgs := args

			// If first arg matches a configured agent, use it as agent name.
			// Otherwise treat all args as prompt for the default agent.
			if len(args) > 0 {
				if _, ok := cfg.Agents[args[0]]; ok {
					agentName = args[0]
					promptArgs = args[1:]
				}
			}
			if agentName == "" {
				return fmt.Errorf("no agents configured")
			}
			agentCfg, ok := cfg.Agents[agentName]
			if !ok {
				return fmt.Errorf("unknown agent %q (configured agents: %s)", agentName, agentNames(cfg))
			}

			// Refresh "always" seed files.
			color.Status("Refreshing %s credentials...", agentName)
			if err := agents.RefreshAlwaysSeeds(cfg.ContainerName, cfg.Agents); err != nil {
				color.Warn("could not refresh seeds: %v", err)
			}

			// Build environment variables (host terminal env + agent-specific).
			env := cfg.HostEnv()
			for k, v := range agentCfg.Env {
				env[k] = v
			}

			// Build the agent command.
			// baseCmd may contain flags (e.g. "claude --dangerously-skip-permissions"),
			// so it's passed raw to the shell. Only the prompt is quoted.
			baseCmd := agentCfg.AgentCmd(agentName)

			// Handle prompt argument.
			promptPart := ""
			if len(promptArgs) > 0 {
				prompt := promptArgs[0]
				if info, err := os.Stat(prompt); err == nil && !info.IsDir() {
					data, err := os.ReadFile(prompt)
					if err != nil {
						return fmt.Errorf("reading prompt file %q: %w", prompt, err)
					}
					prompt = string(data)
				}
				promptPart = " -p " + shellQuote([]string{prompt})
			}

			shellCmd := "cd /workspace && " + baseCmd + promptPart
			opts := incus.UserOpts(cfg.UserHome(), "/workspace")
			opts.Env = env
			return incus.ExecInteractive(ctx, server, cfg.ContainerName, opts, cfg.LoginCmd(shellCmd))
		},
	}
}

func agentNames(cfg *config.MergedConfig) string {
	return strings.Join(slices.Sorted(maps.Keys(cfg.Agents)), ", ")
}
