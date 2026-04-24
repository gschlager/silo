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

			// Start the notification bridge for this session.
			if cfg.Notifications {
				cleanup, err := provision.StartNotifyBridge(cfg.ContainerName)
				if err != nil {
					color.Warn("could not start notifications: %v", err)
				} else {
					defer cleanup()
				}
			}

			opts := incus.UserOpts(cfg.UserHome(), cfg.WorkspacePath())
			opts.Env, err = sessionEnv(cfg)
			if err != nil {
				return err
			}
			return incus.ExecInteractive(ctx, server, cfg.ContainerName, opts,
				[]string{cfg.ShellPath(), "-l"})
		},
	}
}
