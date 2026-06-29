package cli

import (
	"context"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	"github.com/spf13/cobra"
)

func newUpCmd() *cobra.Command {
	var keep bool
	cmd := &cobra.Command{
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

			if !incus.Exists(server, name) {
				// First run: give the project a slot in the central secrets file
				// so the user has an obvious place to add its PAT.
				if added, err := config.EnsureSecretsStub(cfg.ProjectName()); err != nil {
					color.Warn("could not update secrets file: %v", err)
				} else if added {
					color.Info("Added a secrets entry for %q — set its PAT in %s", cfg.ProjectName(), config.SecretsPath())
				}
				// First run: full provisioning.
				return provision.Provision(ctx, server, cfg, keep)
			}

			if incus.IsRunning(server, name) {
				color.Info("Container %s is already running.", name)
				if err := provision.ApplyGitignore(ctx, server, name, cfg.User); err != nil {
					color.Warn("could not apply global gitignore: %v", err)
				}
				// Reconcile daemon units against the current config so daemons
				// added or dropped on another branch are in sync, then (re)start
				// the autostart ones.
				if err := syncDaemons(ctx, server, cfg); err != nil {
					color.Warn("could not sync daemons: %v", err)
				}
				return nil
			}

			// Check if initialized.
			if !provision.IsInitialized(server, name, cfg.User) {
				// Container exists but not initialized — reprovision.
				color.Warn("Container %s exists but is not initialized. Removing and reprovisioning...", name)
				if err := incus.Delete(ctx, server, name); err != nil {
					return err
				}
				return provision.Provision(ctx, server, cfg, keep)
			}

			// Resume: start the stopped container, then refresh the global
			// gitignore so edits to the host file apply on the next start.
			color.Status("Starting %s...", name)
			if err := incus.Start(ctx, server, name); err != nil {
				return err
			}
			if err := provision.ApplyGitignore(ctx, server, name, cfg.User); err != nil {
				color.Warn("could not apply global gitignore: %v", err)
			}
			// Reconcile daemon units against the current config, then start the
			// autostart ones, re-resolving env: and secrets so edits to either
			// (and daemons added or dropped on another branch) take effect here
			// without recreating the container.
			if err := syncDaemons(ctx, server, cfg); err != nil {
				color.Warn("could not sync daemons: %v", err)
			}

			color.Success("Environment ready!")
			return nil
		},
	}
	cmd.Flags().BoolVar(&keep, "keep", false, "Keep the container if provisioning fails, for inspection")
	return cmd
}

// syncDaemons reconciles the installed daemon units with the current config and
// then starts the autostart daemons. Used on `silo up` for an existing container
// so branch switches that add or remove daemons take effect without a recreate.
func syncDaemons(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig) error {
	if err := provision.ReconcileDaemons(ctx, server, cfg.ContainerName, cfg.User, cfg.Shell, cfg.WorkspacePath(), cfg.Daemons); err != nil {
		return err
	}
	return provision.StartConfiguredDaemons(ctx, server, cfg)
}
