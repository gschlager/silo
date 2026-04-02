package cli

import (
	"os"

	"github.com/gschlager/silo/internal/color"
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

			verbose, _ := cmd.Flags().GetBool("verbose")

			if !incus.Exists(server, name) {
				// First run: full provisioning.
				return provision.Provision(ctx, server, cfg, verbose)
			}

			if incus.IsRunning(server, name) {
				color.Info("Container %s is already running.", name)
				return nil
			}

			// Check if initialized.
			if !provision.IsInitialized(ctx, server, name, cfg.User) {
				// Container exists but not initialized — reprovision.
				color.Warn("Container %s exists but is not initialized. Removing and reprovisioning...", name)
				if err := incus.Delete(ctx, server, name); err != nil {
					return err
				}
				return provision.Provision(ctx, server, cfg, verbose)
			}

			// Resume: just start the container.
			color.Status("Starting %s...", name)
			if err := incus.Start(ctx, server, name); err != nil {
				return err
			}

			// Run docker compose on every start if configured.
			if cfg.Compose != "" {
				color.Status("Starting compose services...")
				if err := incus.ExecStreaming(ctx, server, name,
					incus.UserOpts(cfg.UserHome(), "/workspace"),
					cfg.LoginCmd("cd /workspace && docker compose -f "+cfg.Compose+" up -d"),
					os.Stdout, os.Stderr); err != nil {
					color.Warn("compose up failed: %v", err)
				}
			}

			color.Success("Environment ready!")
			return nil
		},
	}
}
