package cli

import (
	"fmt"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newEnterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enter",
		Short: "Open an interactive shell inside the container",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			shell := cfg.Shell
			return incus.ExecInteractive(server, cfg.ContainerName, incus.ExecOpts{
				User:    1000,
				WorkDir: "/workspace",
			}, []string{
				"su", "-", cfg.User, "-c",
				fmt.Sprintf("cd /workspace && exec %s", shell),
			})
		},
	}
}
