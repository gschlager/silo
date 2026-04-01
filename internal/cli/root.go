package cli

import (
	"fmt"
	"os"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/update"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "silo",
		Short: "Secure isolated local environments for AI agents",
		Long: `silo creates isolated development environments using Incus system containers.
It provides full network and service isolation while keeping your existing
workflow (host-side IDE, git client, DB inspector) intact via bind mounts
and port forwarding.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := config.EnsureGlobalConfig(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not create global config: %v\n", err)
			}
			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			// Background update check — non-blocking.
			if Version != "dev" {
				if newVersion := update.CheckForUpdate(Version); newVersion != "" {
					fmt.Fprintf(os.Stderr, "A new version of silo is available (%s). Run 'silo upgrade' to update.\n", newVersion)
				}
			}
		},
	}

	rootCmd.AddCommand(
		newVersionCmd(),
		newUpCmd(),
		newDownCmd(),
		newEnterCmd(),
		newRunCmd(),
		newRmCmd(),
		newPsCmd(),
		newStatusCmd(),
		newRaCmd(),
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newLogsCmd(),
		newSyncCmd(),
		newPullCmd(),
		newResetCmd(),
		newUpdateCmd(),
		newSnapshotCmd(),
		newRestoreCmd(),
		newInitCmd(),
		newConfigCmd(),
		newCompletionCmd(),
		newUpgradeCmd(),
	)

	registerCompletions(rootCmd)

	return rootCmd
}
