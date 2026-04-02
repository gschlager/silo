package cli

import (
	"fmt"
	"os"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show container state, config, and running daemons",
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
				for agentName, agent := range cfg.Agents {
					mode := agent.Mode
					if mode == "" {
						mode = "default"
					}
					status := "enabled"
					if !agent.Enabled {
						status = "disabled"
					}
					fmt.Printf("  %s (mode: %s, %s)\n", agentName, mode, status)
				}
			}

			// Daemons (config + live status if running).
			if len(cfg.Daemons) > 0 {
				fmt.Println("\nDaemons:")
				if inst.Status == "Running" {
					output, err := incus.Exec(ctx, server, name, incus.ExecOpts{}, []string{
						"su", "-", cfg.User, "-c",
						"systemctl --user list-units 'silo-*' --no-pager --no-legend 2>/dev/null || true",
					})
					if err == nil && output != "" {
						fmt.Print(output)
					} else {
						for daemon := range cfg.Daemons {
							fmt.Fprintf(os.Stderr, "  silo-%s (status unknown)\n", daemon)
						}
					}
				} else {
					for daemon, dcfg := range cfg.Daemons {
						autostart := "autostart"
						if !dcfg.Autostart {
							autostart = "manual"
						}
						fmt.Printf("  %s (%s): %s\n", daemon, autostart, dcfg.Cmd)
					}
				}
			}

			return nil
		},
	}
}
