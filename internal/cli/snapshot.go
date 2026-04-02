package cli

import (
	"fmt"
	"time"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot [name]",
		Short: "Take a named snapshot of the container",
		Args:  cobra.MaximumNArgs(1),
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

			if !incus.Exists(server, cfg.ContainerName) {
				return fmt.Errorf("container %s does not exist", cfg.ContainerName)
			}

			name := time.Now().Format("2006-01-02-150405")
			if len(args) > 0 {
				name = args[0]
			}

			fmt.Printf("Creating snapshot %q...\n", name)
			if err := incus.CreateSnapshot(ctx, server, cfg.ContainerName, name); err != nil {
				return err
			}
			fmt.Println("Done.")
			return nil
		},
	}

	cmd.AddCommand(newSnapshotListCmd())
	return cmd
}

func newSnapshotListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available snapshots",
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

			if !incus.Exists(server, cfg.ContainerName) {
				return fmt.Errorf("container %s does not exist", cfg.ContainerName)
			}

			snapshots, err := incus.ListSnapshots(server, cfg.ContainerName)
			if err != nil {
				return err
			}

			if len(snapshots) == 0 {
				fmt.Println("No snapshots.")
				return nil
			}

			for _, s := range snapshots {
				fmt.Printf("%-30s  %s\n", s.Name, s.CreatedAt)
			}
			return nil
		},
	}
}

func newRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <name>",
		Short: "Restore the container to a snapshot",
		Args:  cobra.ExactArgs(1),
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

			if !incus.Exists(server, cfg.ContainerName) {
				return fmt.Errorf("container %s does not exist", cfg.ContainerName)
			}

			name := args[0]
			fmt.Printf("Restoring snapshot %q...\n", name)
			if err := incus.RestoreSnapshot(ctx, server, cfg.ContainerName, name); err != nil {
				return err
			}
			fmt.Println("Done.")
			return nil
		},
	}
}
