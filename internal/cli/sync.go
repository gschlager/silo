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
			ctx := cmd.Context()

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

			if err := ensureRunning(ctx, server, cfg.ContainerName); err != nil {
				return err
			}

			opts := incus.UserOpts(cfg.UserHome(), cfg.WorkspacePath())
			for _, syncCmd := range cfg.Sync {
				color.Status("%s", syncCmd)
				if err := incus.ExecStreaming(ctx, server, cfg.ContainerName, opts,
					cfg.LoginCmd("cd "+cfg.WorkspacePath()+" && "+syncCmd),
					os.Stdout, os.Stderr); err != nil {
					return fmt.Errorf("sync command %q: %w", syncCmd, err)
				}
			}
			return nil
		},
	}
}
