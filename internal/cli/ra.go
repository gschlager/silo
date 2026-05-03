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
	"github.com/gschlager/silo/internal/provision"
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
			// With DisableFlagParsing, cobra hands us every token verbatim —
			// including root persistent flags like --verbose. Handle the ones
			// that belong to silo here, and strip them so they don't leak
			// through to the agent. Use `--` to pass them to the agent instead.
			var filtered []string
			for _, a := range args {
				switch a {
				case "--help", "-h":
					return cmd.Help()
				case "--verbose", "-v":
					color.EnableDebug()
				default:
					filtered = append(filtered, a)
				}
			}
			args = filtered

			ctx := cmd.Context()

			color.Debug("loading config")
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			color.Debug("connecting to incus")
			server, err := incus.Connect()
			if err != nil {
				return err
			}

			color.Debug("ensuring container %s is running", cfg.ContainerName)
			if err := ensureRunning(ctx, server, cfg.ContainerName); err != nil {
				return err
			}

			// Start the notification bridge for this session.
			if cfg.Notifications {
				color.Debug("starting notification bridge")
				cleanup, err := provision.StartNotifyBridge(cfg.ContainerName)
				if err != nil {
					color.Warn("could not start notifications: %v", err)
				} else {
					defer cleanup()
				}
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
			color.Debug("creating pre-session snapshot")
			snapName := "pre-session-" + time.Now().Format("20060102-150405")
			if err := incus.CreateSnapshot(ctx, server, cfg.ContainerName, snapName); err != nil {
				color.Warn("could not create pre-session snapshot: %v", err)
			} else {
				incus.CleanupSnapshots(ctx, server, cfg.ContainerName, "pre-session-", 3)
			}

			// Ensure the agent mode directory exists and is seeded.
			color.Debug("preparing agent dir for %s (mode=%s)", agentName, agentCfg.Mode)
			agents.EnsureModeDir(agentName, agentCfg.Mode, agentCfg.Links)

			// Build environment variables (host env + tool credentials + agent-specific).
			color.Debug("resolving session env (tool credentials)")
			env, err := sessionEnv(cfg)
			if err != nil {
				return err
			}
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
			color.Debug("exec %s (terminal goes raw — no further output)", agentName)
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
