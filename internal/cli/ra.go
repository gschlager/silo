package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/gschlager/silo/internal/agents"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newRaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ra [agent] [args...]",
		Short: "Run an AI agent interactively inside the container",
		Long: `Run an AI agent interactively inside the container.
Copies agent-specific auth/config, then launches the agent with TTY attached.
Without arguments, runs the default agent.
Arguments after the agent name (or after --) are passed to the agent.

Examples:
  silo ra
  silo ra claude
  silo ra claude "fix the failing tests"
  silo ra claude --resume
  silo ra --resume                  (default agent with --resume)
  silo ra claude --resume -p "fix"`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// With DisableFlagParsing, handle --help manually.
			for _, a := range args {
				if a == "--help" || a == "-h" {
					return cmd.Help()
				}
			}

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
			var extraArgs []string

			// If first arg matches a configured agent, use it as agent name.
			// Otherwise treat all args as passthrough to the default agent.
			if len(args) > 0 {
				if _, ok := cfg.Agents[args[0]]; ok {
					agentName = args[0]
					extraArgs = args[1:]
				} else {
					extraArgs = args
				}
			}
			if agentName == "" {
				return fmt.Errorf("no agents configured")
			}
			agentCfg, ok := cfg.Agents[agentName]
			if !ok {
				return fmt.Errorf("unknown agent %q (configured agents: %s)", agentName, agentNames(cfg))
			}

			// Take a pre-session snapshot for rollback, then clean up old ones.
			snapName := "pre-session-" + time.Now().Format("20060102-150405")
			if err := incus.CreateSnapshot(ctx, server, cfg.ContainerName, snapName); err != nil {
				color.Warn("could not create pre-session snapshot: %v", err)
			} else {
				incus.CleanupSnapshots(ctx, server, cfg.ContainerName, "pre-session-", 3)
			}

			// Ensure the agent mode directory exists and is seeded.
			agents.EnsureModeDir(agentName, agentCfg.Mode, agentCfg.Links)

			// Build environment variables (host env + tool credentials + agent-specific).
			env := sessionEnv(cfg)
			for k, v := range agentCfg.Env {
				env[k] = v
			}

			// Build the agent command with passthrough args.
			shellCmd := "cd " + cfg.WorkspacePath() + " && " + agentCfg.AgentCmd(agentName)
			if len(extraArgs) > 0 {
				shellCmd += " " + shellQuote(extraArgs)
			}

			opts := incus.UserOpts(cfg.UserHome(), cfg.WorkspacePath())
			opts.Env = env
			return incus.ExecInteractive(ctx, server, cfg.ContainerName, opts, cfg.LoginCmd(shellCmd))
		},
	}

	// Allow flags like --resume to pass through to the agent instead of
	// being parsed by cobra.
	cmd.DisableFlagParsing = true

	return cmd
}

func agentNames(cfg *config.MergedConfig) string {
	return strings.Join(slices.Sorted(maps.Keys(cfg.Agents)), ", ")
}
