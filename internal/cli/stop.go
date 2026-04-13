package cli

import (
	"fmt"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <daemon>",
		Short: "Stop a running daemon",
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

			if err := requireRunning(server, cfg.ContainerName); err != nil {
				return err
			}

			daemon := args[0]
			if _, ok := cfg.Daemons[daemon]; !ok {
				return fmt.Errorf("unknown daemon %q", daemon)
			}

			_, err = incus.Exec(ctx, server, cfg.ContainerName, incus.ExecOpts{},
				systemctlUser(cfg.User, "stop", "silo-"+daemon))
			return err
		},
	}
}
