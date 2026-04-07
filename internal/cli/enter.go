package cli

import (
	"github.com/gschlager/silo/internal/incus"
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

			if err := requireRunning(server, cfg.ContainerName); err != nil {
				return err
			}

			opts := incus.UserOpts(cfg.UserHome(), "/workspace")
			opts.Env = sessionEnv(cfg)
			return incus.ExecInteractive(ctx, server, cfg.ContainerName, opts,
				[]string{cfg.ShellPath(), "-l"})
		},
	}
}
