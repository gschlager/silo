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
	var yes bool

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
				fmt.Fprintf(os.Stderr, "Remove container %s and all its data? [y/N] ", name)
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

			// Ask about agent data.
			if len(cfg.Agents) > 0 {
				deleteAgents := yes
				if !yes {
					fmt.Fprintf(os.Stderr, "Also delete agent data directories (sessions, memory)? [y/N] ")
					reader := bufio.NewReader(os.Stdin)
					answer, _ := reader.ReadString('\n')
					deleteAgents = strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y")
				}
				if deleteAgents {
					var agentNames []string
					for name := range cfg.Agents {
						agentNames = append(agentNames, name)
					}
					if err := agents.CleanupAgentDirs(cfg.ContainerName, agentNames); err != nil {
						return err
					}
					fmt.Fprintln(os.Stderr, "Agent data removed.")
				}
			}

			fmt.Fprintln(os.Stderr, "Done.")
			return nil
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompts")
	return cmd
}
