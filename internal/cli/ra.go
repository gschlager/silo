package cli

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/gschlager/silo/internal/agents"
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

			// Sync shared files (credentials, settings) into the container dir.
			agents.SyncToContainer(agentName, cfg.ContainerName, agentCfg.Shared)

			// Build environment variables (host terminal env + agent-specific).
			env := cfg.HostEnv()
			for k, v := range agentCfg.Env {
				env[k] = v
			}

			// Build the agent command.
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
			err = incus.ExecInteractive(ctx, server, cfg.ContainerName, opts, cfg.LoginCmd(shellCmd))

			// Sync shared files back (pick up token refreshes, setting changes).
			agents.SyncFromContainer(agentName, cfg.ContainerName, agentCfg.Shared)

			return err
		},
	}
}

func agentNames(cfg *config.MergedConfig) string {
	return strings.Join(slices.Sorted(maps.Keys(cfg.Agents)), ", ")
}
