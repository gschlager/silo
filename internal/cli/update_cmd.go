package cli

import (
	"fmt"
	"os"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Run the update commands (system-level updates)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			if len(cfg.Update) == 0 {
				return fmt.Errorf("no update commands configured")
			}

			server, err := incus.Connect()
			if err != nil {
				return err
			}

			if err := requireRunning(server, cfg.ContainerName); err != nil {
				return err
			}

			opts := incus.UserOpts(cfg.UserHome(), "/workspace")
			for _, updateCmd := range cfg.Update {
				fmt.Fprintf(os.Stderr, "==> %s\n", updateCmd)
				if err := incus.ExecStreaming(server, cfg.ContainerName, opts,
					cfg.LoginCmd("cd /workspace && "+updateCmd),
					os.Stdout, os.Stderr); err != nil {
					return fmt.Errorf("update command %q: %w", updateCmd, err)
				}
			}
			return nil
		},
	}
}
