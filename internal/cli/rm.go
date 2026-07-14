package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/gschlager/silo/internal/agents"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newRmCmd() *cobra.Command {
	var yes, purge bool

	cmd := &cobra.Command{
		Use:   "rm",
		Short: "Remove the container and its data",
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

			if !yes {
				kept := "project files, agent credentials, and the selected mode are kept"
				if purge {
					kept = "project files and agent credentials are kept; the selected mode is reset"
				}
				fmt.Fprintf(os.Stderr, "Remove %s? (%s) [y/N] ", name, kept)
				reader := bufio.NewReader(os.Stdin)
				answer, _ := reader.ReadString('\n')
				if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
					fmt.Fprintln(os.Stderr, "Aborted.")
					return nil
				}
			}

			// Stop if running.
			if incus.IsRunning(server, name) {
				color.Status("Stopping %s...", name)
				if err := incus.Stop(ctx, server, name); err != nil {
					return err
				}
			}

			// Delete container.
			color.Status("Removing %s...", name)
			if err := incus.Delete(ctx, server, name); err != nil {
				return err
			}

			// Clean up per-container state. The mode selection is kept so a later
			// `silo up` restores it, unless --purge asks for a full reset.
			agents.CleanupContainerDirs(cfg.ContainerName, purge)

			fmt.Fprintln(os.Stderr, "Done.")
			return nil
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompts")
	cmd.Flags().BoolVar(&purge, "purge", false, "Also reset the selected agent mode to the config default")
	return cmd
}
