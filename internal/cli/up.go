package cli

import (
	"fmt"
	"os"

	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	"github.com/spf13/cobra"
)

func newUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Start the environment for the current project",
		Long: `Start the environment for the current project.
First run: create container, provision, run setup.
Subsequent: start the stopped container (~1 second).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
				// First run: full provisioning.
				return provision.Provision(server, cfg)
			}

			if incus.IsRunning(server, name) {
				fmt.Fprintf(os.Stderr, "Container %s is already running.\n", name)
				return nil
			}

			// Check if initialized.
			if !provision.IsInitialized(server, name, cfg.User) {
				// Container exists but not initialized — reprovision.
				fmt.Fprintf(os.Stderr, "Container %s exists but is not initialized. Removing and reprovisioning...\n", name)
				if err := incus.Delete(server, name); err != nil {
					return err
				}
				return provision.Provision(server, cfg)
			}

			// Resume: just start the container.
			fmt.Fprintf(os.Stderr, "Starting %s...\n", name)
			if err := incus.Start(server, name); err != nil {
				return err
			}

			// Run docker compose on every start if configured.
			if cfg.Compose != "" {
				fmt.Fprintf(os.Stderr, "Starting compose services...\n")
				if err := incus.ExecStreaming(server, name, incus.ExecOpts{
					User: 1000, WorkDir: "/workspace",
				}, []string{
					"su", "-", cfg.User, "-c",
					"cd /workspace && docker compose -f " + cfg.Compose + " up -d",
				}, os.Stdout, os.Stderr); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: compose up failed: %v\n", err)
				}
			}

			fmt.Fprintf(os.Stderr, "Environment ready!\n")
			return nil
		},
	}
}
