package cli

import (
	"fmt"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/spf13/cobra"
)

func newModeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mode [agent] [mode]",
		Short: "Show or switch the authentication mode for an agent",
		Long: `Show or switch the authentication mode for an agent.

Without arguments, shows the current mode for all agents.
With one argument, shows the current mode for that agent.
With two arguments, switches the agent to the given mode.

Each mode gets its own isolated data directory (history, settings, credentials).

Modes for Claude: claude, console, bedrock, vertex, foundry

Examples:
  silo mode
  silo mode claude
  silo mode claude bedrock`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			if len(args) == 0 {
				// Show all agent modes.
				for _, name := range cfg.AgentOrder {
					agent := cfg.Agents[name]
					if agent.Enabled {
						fmt.Printf("  %-16s %s\n", name, agent.Mode)
					}
				}
				return nil
			}

			agentName := args[0]
			agent, ok := cfg.Agents[agentName]
			if !ok {
				return fmt.Errorf("unknown agent %q", agentName)
			}

			if len(args) == 1 {
				// Show mode for one agent.
				fmt.Printf("  %-16s %s\n", agentName, agent.Mode)
				return nil
			}

			// Switch mode.
			newMode := args[1]
			if newMode == agent.Mode {
				color.Info("%s is already using mode %q.", agentName, newMode)
				return nil
			}

			modes, err := config.LoadModeState(cfg.ContainerName)
			if err != nil {
				return err
			}
			modes[agentName] = newMode
			if err := config.SaveModeState(cfg.ContainerName, modes); err != nil {
				return err
			}

			color.Success("Switched %s to %s mode.", agentName, newMode)
			color.Info("Run 'silo restart' for the change to take effect.")
			return nil
		},
	}
}
