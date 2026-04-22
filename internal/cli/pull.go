package cli

import (
	"fmt"
	"os"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Run git pull inside the container, then run sync",
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

			opts := incus.UserOpts(cfg.UserHome(), cfg.WorkspacePath())
			cdWs := "cd " + cfg.WorkspacePath() + " && "

			// Run git pull.
			color.Status("git pull")
			if err := incus.ExecStreaming(ctx, server, cfg.ContainerName, opts,
				cfg.LoginCmd(cdWs+"git pull"),
				os.Stdout, os.Stderr); err != nil {
				return fmt.Errorf("git pull: %w", err)
			}

			// Run sync commands.
			if len(cfg.Sync) > 0 {
				for _, syncCmd := range cfg.Sync {
					color.Status("%s", syncCmd)
					if err := incus.ExecStreaming(ctx, server, cfg.ContainerName, opts,
						cfg.LoginCmd(cdWs+syncCmd),
						os.Stdout, os.Stderr); err != nil {
						return fmt.Errorf("sync command %q: %w", syncCmd, err)
					}
				}
			}

			return nil
		},
	}
}
