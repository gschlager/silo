package cli

import (
	"fmt"
	"os"

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

			opts := incus.ExecOpts{User: 1000, WorkDir: "/workspace"}
			for _, cmd := range cfg.Sync {
				fmt.Fprintf(os.Stderr, "==> %s\n", cmd)
				if err := incus.ExecStreaming(server, cfg.ContainerName, opts,
					[]string{"su", "-", cfg.User, "-c", "cd /workspace && " + cmd},
					os.Stdout, os.Stderr); err != nil {
					return fmt.Errorf("sync command %q: %w", cmd, err)
				}
			}
			return nil
		},
	}
}
