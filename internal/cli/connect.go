package cli

import (
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/gschlager/silo/internal/agents"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	"github.com/spf13/cobra"
)

func newConnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "connect <project-dir> <shell-line>",
		Short: "Run a shell line in the container over plain stdio (used by herdr)",
		Long: `Run a shell line in the project's container over plain stdio.
This is the connection command silo herdr registers with herdr-core. It starts
the container if needed and runs the line in a login shell as the agent user,
with the project's secrets and the agent mode env, but without a TTY: stdin and
stdout carry herdr-core's byte protocol, so nothing else may be written to
stdout.`,
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// herdr-core runs this from wherever it was started, so the project
			// is identified by its directory, not by the current one.
			if err := os.Chdir(args[0]); err != nil {
				return fmt.Errorf("changing to project dir: %w", err)
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			server, err := incus.Connect()
			if err != nil {
				return err
			}

			if err := ensureRunning(ctx, server, cfg.ContainerName); err != nil {
				return err
			}

			// Credentials only: pass_env is about the host terminal, and the
			// terminals here belong to herdr, not to whoever started its core.
			env, err := provision.ResolveSessionEnv(cfg)
			if err != nil {
				return err
			}

			// The core starts whichever agent herdr asks for, so every enabled
			// agent gets its mode dir and mode env.
			for _, name := range slices.Sorted(maps.Keys(cfg.Agents)) {
				agentCfg := cfg.Agents[name]
				if !agentCfg.Enabled {
					continue
				}
				if err := agents.EnsureModeDir(name, agentCfg.Mode); err != nil {
					return err
				}
				agentEnv, err := provision.ResolveAgentEnv(agentCfg)
				if err != nil {
					return err
				}
				maps.Copy(env, agentEnv)
			}

			opts := incus.UserOpts(cfg.UserHome(), cfg.WorkspacePath())
			opts.Env = env
			color.Debug("exec %s -lc %q over stdio", cfg.ShellPath(), args[1])
			return incus.ExecPipe(ctx, server, cfg.ContainerName, opts, cfg.LoginCmd(args[1]))
		},
	}
}
