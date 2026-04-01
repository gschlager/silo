package cli

import (
	"fmt"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart [daemon]",
		Short: "Restart the container or a specific daemon",
		Long: `Without arguments, restarts the container.
With a daemon name, restarts that specific daemon.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			server, err := incus.Connect()
			if err != nil {
				return err
			}

			if len(args) == 0 {
				// Restart container.
				if err := requireRunning(server, cfg.ContainerName); err != nil {
					return err
				}
				color.Status("Restarting %s...", cfg.ContainerName)
				return incus.Restart(server, cfg.ContainerName)
			}

			// Restart daemon.
			if err := requireRunning(server, cfg.ContainerName); err != nil {
				return err
			}

			daemon := args[0]
			if _, ok := cfg.Daemons[daemon]; !ok {
				return fmt.Errorf("unknown daemon %q", daemon)
			}

			_, err = incus.Exec(server, cfg.ContainerName, incus.ExecOpts{}, []string{
				"su", "-", cfg.User, "-c",
				fmt.Sprintf("systemctl --user restart silo-%s", daemon),
			})
			return err
		},
	}
}
