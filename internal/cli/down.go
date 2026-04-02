package cli

import (
	"fmt"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	"github.com/spf13/cobra"
)

func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop the container (preserves all state)",
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

			name := cfg.ContainerName

			if !incus.Exists(server, name) {
				return fmt.Errorf("container %s does not exist", name)
			}

			if !incus.IsRunning(server, name) {
				color.Info("Container %s is already stopped.", name)
				return nil
			}

			color.Status("Stopping %s...", name)
			if err := incus.Stop(ctx, server, name); err != nil {
				return err
			}

			// Clean up notification socket.
			provision.CleanupNotifications(name)
			return nil
		},
	}
}
