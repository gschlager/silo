package cli

import (
	"context"
	"fmt"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/configupdate"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	incuscli "github.com/lxc/incus/v7/client"
	"github.com/spf13/cobra"
)

func newUpCmd() *cobra.Command {
	var keep, fresh bool
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the environment for the current project",
		Long: `Start the environment for the current project.
First run: create container, provision, run setup.
Subsequent: start the stopped container (~1 second).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			ctx := cmd.Context()

			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			hintConfigUpdate(cfg)

			server, err := incus.Connect()
			if err != nil {
				return err
			}

			// Once the container runs, refresh the agent functions and make sure
			// herdr knows it (if installed).
			defer func() {
				if err == nil {
					writeAgentFunctions(ctx, server, cfg)
					ensureHerdrMachine(cfg)
				}
			}()

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
				return provision.Provision(ctx, server, cfg, provision.Options{Keep: keep, FreshBase: fresh})
			}

			if fresh {
				color.Warn("--fresh only applies when creating a container; use `silo update` for this one.")
			}

			if incus.IsRunning(server, name) {
				color.Info("Container %s is already running.", name)
				setupHostSocket(ctx, server, cfg)
				if err := provision.ApplyGitignore(ctx, server, name, cfg.User); err != nil {
					color.Warn("could not apply global gitignore: %v", err)
				}
				// Reconcile port forwards (hot-plugged here) and daemon units
				// against the current config, then (re)start the autostart
				// daemons. A proxy device added on a branch switch only binds
				// reliably while the container is stopped, so if a new port stays
				// unreachable, `silo down && silo up` applies it cleanly.
				if err := provision.ReconcilePorts(ctx, server, name, cfg.Ports); err != nil {
					color.Warn("could not sync port forwards: %v", err)
				}
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
				return provision.Provision(ctx, server, cfg, provision.Options{Keep: keep, FreshBase: fresh})
			}

			// Resume: reconcile port forwards while the container is still
			// stopped, so a proxy device added on another branch binds cleanly on
			// start (the same way first-time provisioning adds them). This is what
			// makes `silo down && silo up` pick up a new daemon's port.
			if err := provision.ReconcilePorts(ctx, server, name, cfg.Ports); err != nil {
				color.Warn("could not sync port forwards: %v", err)
			}
			color.Status("Starting %s...", name)
			provision.MarkStarting(name)
			if err := incus.Start(ctx, server, name); err != nil {
				return err
			}
			setupHostSocket(ctx, server, cfg)
			if err := provision.ApplyGitignore(ctx, server, name, cfg.User); err != nil {
				color.Warn("could not apply global gitignore: %v", err)
			}
			// Reconcile daemon units (needs the container running — it execs
			// systemctl inside it) and start the autostart ones, re-resolving
			// env: and secrets so edits to either take effect without a recreate.
			if err := syncDaemons(ctx, server, cfg); err != nil {
				color.Warn("could not sync daemons: %v", err)
			}

			color.Success("Environment ready!")
			return nil
		},
	}
	cmd.Flags().BoolVar(&keep, "keep", false, "Keep the container if provisioning fails, for inspection")
	cmd.Flags().BoolVar(&fresh, "fresh", false, "Rebuild the base template first, for the latest packages and agents")
	return cmd
}

// syncDaemons reconciles the installed daemon units with the current config and
// starts the autostart daemons. The container must be running — it execs
// systemctl --user inside it. Port forwards are reconciled separately by the
// caller, before the container starts, so the proxy devices bind cleanly.
func syncDaemons(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig) error {
	if err := provision.ReconcileDaemons(ctx, server, cfg.ContainerName, cfg.User, cfg.Shell, cfg.WorkspacePath(), cfg.Daemons); err != nil {
		return err
	}
	return provision.StartConfiguredDaemons(ctx, server, cfg)
}

// setupHostSocket makes sure the container has its socket to the silo service,
// current helpers and the commit signing settings. Containers created before
// the service get it here.
func setupHostSocket(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig) {
	if err := provision.SetupHostSocket(ctx, server, cfg.ContainerName); err != nil {
		color.Warn("could not connect the silo service: %v", err)
		return
	}
	if err := provision.SetupSigning(ctx, server, cfg.ContainerName, cfg.User); err != nil {
		color.Warn("could not set up commit signing: %v", err)
	}
}

// hintConfigUpdate mentions `silo config update` when it has suggestions
// for the project's config.
func hintConfigUpdate(cfg *config.MergedConfig) {
	ss, err := configupdate.Check(cfg.ProjectDir, cfg.ProjectName())
	if err != nil || len(ss) == 0 {
		return
	}
	what := ss[0].Title
	if len(ss) > 1 {
		what = fmt.Sprintf("%s, and %d more", what, len(ss)-1)
	}
	color.Info("silo config update has suggestions for this project: %s.", what)
}
