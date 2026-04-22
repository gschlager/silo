package cli

import (
	"fmt"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <daemon>",
		Short: "Start a daemon",
		Args:  requireArgs(1, "daemon name"),
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

			daemon := args[0]
			if _, ok := cfg.Daemons[daemon]; !ok {
				return fmt.Errorf("unknown daemon %q", daemon)
			}

			_, err = incus.Exec(ctx, server, cfg.ContainerName, incus.ExecOpts{}, []string{
				"su", "-", cfg.User, "-c",
				fmt.Sprintf("systemctl --user start silo-%s", daemon),
			})
			return err
		},
	}
}
