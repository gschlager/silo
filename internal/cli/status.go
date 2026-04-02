package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

var (
	statusLabel = lipgloss.NewStyle().Bold(true).Width(12)
	statusSection = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")) // yellow
	statusGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	statusRed   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	statusDim   = lipgloss.NewStyle().Faint(true)
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
				fmt.Printf("%s %s\n", statusLabel.Render("Container:"), name)
				fmt.Printf("%s %s\n", statusLabel.Render("Status:"), statusDim.Render("not created"))
				return nil
			}

			inst, err := incus.GetInstance(server, name)
			if err != nil {
				return err
			}

			// Container info.
			containerStatus := statusRed.Render(inst.Status)
			if inst.Status == "Running" {
				containerStatus = statusGreen.Render(inst.Status)
			}
			fmt.Printf("%s %s\n", statusLabel.Render("Container:"), name)
			fmt.Printf("%s %s\n", statusLabel.Render("Status:"), containerStatus)
			fmt.Printf("%s %s\n", statusLabel.Render("Image:"), cfg.Image)
			fmt.Printf("%s %s\n", statusLabel.Render("User:"), cfg.User)
			fmt.Printf("%s %s\n", statusLabel.Render("Shell:"), cfg.Shell)
			fmt.Printf("%s %s\n", statusLabel.Render("Project:"), cfg.ProjectDir)

			// Port mappings.
			if len(cfg.Ports) > 0 {
				fmt.Printf("\n%s\n", statusSection.Render("Ports"))
				for _, p := range cfg.Ports {
					parts := strings.SplitN(p, ":", 2)
					if len(parts) == 2 {
						fmt.Printf("  container:%s → localhost:%s\n", parts[0], parts[1])
					} else {
						fmt.Printf("  %s\n", p)
					}
				}
			}

			// Agents.
			if len(cfg.Agents) > 0 {
				fmt.Printf("\n%s\n", statusSection.Render("Agents"))
				for agentName, agent := range cfg.Agents {
					mode := statusDim.Render(agent.Mode)
					state := statusGreen.Render("enabled")
					if !agent.Enabled {
						state = statusRed.Render("disabled")
					}
					fmt.Printf("  %-16s %s  %s\n", agentName, state, mode)
				}
			}

			// Daemons.
			if len(cfg.Daemons) > 0 {
				fmt.Printf("\n%s\n", statusSection.Render("Daemons"))
				for daemon, dcfg := range cfg.Daemons {
					state := statusDim.Render("stopped")
					if inst.Status == "Running" {
						out, err := incus.Exec(ctx, server, name, incus.ExecOpts{}, []string{
							"su", "-", cfg.User, "-c",
							fmt.Sprintf("systemctl --user is-active silo-%s 2>/dev/null || true", daemon),
						})
						if err == nil {
							switch strings.TrimSpace(out) {
							case "active":
								state = statusGreen.Render("running")
							case "failed":
								state = statusRed.Render("failed")
							}
						}
					} else {
						state = statusDim.Render("container stopped")
					}
					extra := ""
					if !dcfg.Autostart {
						extra = statusDim.Render("  (manual)")
					}
					fmt.Printf("  %-16s %s%s\n", daemon, state, extra)
				}
			}

			// Mounts.
			if len(cfg.Mounts) > 0 {
				fmt.Printf("\n%s\n", statusSection.Render("Mounts"))
				for _, m := range cfg.Mounts {
					fmt.Printf("  %s\n", m)
				}
			}

			return nil
		},
	}
}
