package cli

import (
	"fmt"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show container state, daemons, port mappings, agent config",
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

			name := cfg.ContainerName

			if !incus.Exists(server, name) {
				fmt.Printf("Container: %s\n", name)
				fmt.Printf("Status:    not created\n")
				return nil
			}

			inst, err := incus.GetInstance(server, name)
			if err != nil {
				return err
			}

			fmt.Printf("Container: %s\n", name)
			fmt.Printf("Status:    %s\n", inst.Status)
			fmt.Printf("Image:     %s\n", cfg.Image)
			fmt.Printf("User:      %s\n", cfg.User)
			fmt.Printf("Shell:     %s\n", cfg.Shell)
			fmt.Printf("Project:   %s\n", cfg.ProjectDir)

			// Port mappings.
			if len(cfg.Ports) > 0 {
				fmt.Println("\nPorts:")
				for _, p := range cfg.Ports {
					fmt.Printf("  %s\n", p)
				}
			}

			// Mounts.
			if len(cfg.Mounts) > 0 {
				fmt.Println("\nMounts:")
				for _, m := range cfg.Mounts {
					fmt.Printf("  %s\n", m)
				}
			}

			// Agents.
			if len(cfg.Agents) > 0 {
				fmt.Println("\nAgents:")
				for name, agent := range cfg.Agents {
					mode := agent.Mode
					if mode == "" {
						mode = "default"
					}
					fmt.Printf("  %s (mode: %s)\n", name, mode)
				}
			}

			// Daemons.
			if len(cfg.Daemons) > 0 {
				fmt.Println("\nDaemons:")
				for name, daemon := range cfg.Daemons {
					autostart := "autostart"
					if !daemon.Autostart {
						autostart = "manual"
					}
					fmt.Printf("  %s (%s): %s\n", name, autostart, daemon.Cmd)
				}
			}

			return nil
		},
	}
}
