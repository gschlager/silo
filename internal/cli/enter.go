package cli

import (
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	"github.com/spf13/cobra"
)

func newEnterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enter",
		Short: "Open an interactive shell inside the container",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			// Refresh the global gitignore so edits to the host file apply on enter.
			if err := provision.ApplyGitignore(ctx, server, cfg.ContainerName, cfg.User); err != nil {
				color.Warn("could not apply global gitignore: %v", err)
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

			color.Debug("resolving session env (tool credentials)")
			opts := incus.UserOpts(cfg.UserHome(), cfg.WorkspacePath())
			opts.Env, err = sessionEnv(cfg)
			if err != nil {
				return err
			}

			color.Debug("exec %s -l (terminal goes raw — no further output)", cfg.ShellPath())
			return incus.ExecInteractive(ctx, server, cfg.ContainerName, opts,
				[]string{cfg.ShellPath(), "-l"})
		},
	}
}
