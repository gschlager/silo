package cli

import (
	"fmt"
	"os"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Run the sync commands (after pulling new code)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			if len(cfg.Sync) == 0 {
				return fmt.Errorf("no sync commands configured")
			}

			server, err := incus.Connect()
			if err != nil {
				return err
			}

			if err := requireRunning(server, cfg.ContainerName); err != nil {
				return err
			}

			opts := incus.UserOpts(cfg.UserHome(), "/workspace")
			for _, cmd := range cfg.Sync {
				color.Status("%s", cmd)
				if err := incus.ExecStreaming(server, cfg.ContainerName, opts,
					cfg.LoginCmd("cd /workspace && "+cmd),
					os.Stdout, os.Stderr); err != nil {
					return fmt.Errorf("sync command %q: %w", cmd, err)
				}
			}
			return nil
		},
	}
}
