package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

var (
	statusLabel   = lipgloss.NewStyle().Bold(true).Width(12)
	statusSection = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")) // yellow
	statusGreen   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	statusRed     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	statusDim     = lipgloss.NewStyle().Faint(true)
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
			exists := incus.Exists(server, name)
			running := exists && incus.IsRunning(server, name)

			// Container info — always shown.
			containerStatus := statusDim.Render("not created")
			if exists {
				containerStatus = statusRed.Render("Stopped")
				if running {
					containerStatus = statusGreen.Render("Running")
				}
			}
			fmt.Printf("%s %s\n", statusLabel.Render("Container:"), name)
			fmt.Printf("%s %s\n", statusLabel.Render("Status:"), containerStatus)
			fmt.Printf("%s %s\n", statusLabel.Render("Image:"), cfg.Image)
			fmt.Printf("%s %s\n", statusLabel.Render("User:"), cfg.User)
			fmt.Printf("%s %s\n", statusLabel.Render("Shell:"), cfg.Shell)
			fmt.Printf("%s %s\n", statusLabel.Render("Project:"), cfg.ProjectDir)

			// Runtime info (only when running).
			if running {
				if inst, err := incus.GetInstance(server, name); err == nil {
					if state, err := incus.GetInstanceState(server, name); err == nil {
						if state.Memory.Usage > 0 {
							fmt.Printf("%s %s\n", statusLabel.Render("Memory:"), formatBytes(state.Memory.Usage))
						}

						if eth0, ok := state.Network["eth0"]; ok {
							for _, addr := range eth0.Addresses {
								if addr.Family == "inet" && addr.Scope == "global" {
									fmt.Printf("%s %s\n", statusLabel.Render("IP:"), addr.Address)
									break
								}
							}
						}

						if state.Pid > 0 && !inst.LastUsedAt.IsZero() {
							uptime := time.Since(inst.LastUsedAt).Truncate(time.Second)
							fmt.Printf("%s %s\n", statusLabel.Render("Uptime:"), formatDuration(uptime))
						}
					}
				}
			}

			// Snapshots.
			if exists {
				if count := incus.SnapshotCount(server, name); count > 0 {
					fmt.Printf("%s %d\n", statusLabel.Render("Snapshots:"), count)
				}
			}

			// Port mappings — from config.
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

			// Agents — from config.
			if len(cfg.Agents) > 0 {
				fmt.Printf("\n%s\n", statusSection.Render("Agents"))
				for _, agentName := range cfg.AgentOrder {
					agent, ok := cfg.Agents[agentName]
					if !ok {
						continue
					}
					mode := statusDim.Render(agent.Mode)
					state := statusGreen.Render("enabled")
					if !agent.Enabled {
						state = statusRed.Render("disabled")
					}
					fmt.Printf("  %-16s %s  %s\n", agentName, state, mode)
				}
			}

			// Daemons — from config, with live status when running.
			if len(cfg.Daemons) > 0 {
				fmt.Printf("\n%s\n", statusSection.Render("Daemons"))
				for daemon, dcfg := range cfg.Daemons {
					state := statusDim.Render("stopped")
					if running {
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
					} else if exists {
						state = statusDim.Render("container stopped")
					}
					extra := ""
					if !dcfg.Autostart {
						extra = statusDim.Render("  (manual)")
					}
					fmt.Printf("  %-16s %s%s\n", daemon, state, extra)
				}
			}

			// Mounts — from config.
			if len(cfg.Mounts) > 0 {
				fmt.Printf("\n%s\n", statusSection.Render("Mounts"))
				for _, m := range cfg.Mounts {
					fmt.Printf("  %s\n", m)
				}
			}

			// Environment variables — from config (names only, values may be secrets).
			if len(cfg.Env) > 0 {
				fmt.Printf("\n%s\n", statusSection.Render("Environment"))
				for _, k := range sortedKeys(cfg.Env) {
					fmt.Printf("  %s\n", k)
				}
			}

			return nil
		},
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Use simple sort since maps package may not have sorted keys helper.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

func formatBytes(b int64) string {
	const (
		mib = 1024 * 1024
		gib = 1024 * mib
	)
	switch {
	case b >= gib:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(gib))
	case b >= mib:
		return fmt.Sprintf("%.0f MiB", float64(b)/float64(mib))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
