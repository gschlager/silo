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

			// Runtime info (only when running).
			if inst.Status == "Running" {
				if state, err := incus.GetInstanceState(server, name); err == nil {
					// Memory.
					if state.Memory.Usage > 0 {
						fmt.Printf("%s %s\n", statusLabel.Render("Memory:"), formatBytes(state.Memory.Usage))
					}

					// Disk usage from storage volume.
					if usage, err := incus.GetVolumeUsage(server, name); err == nil && usage > 0 {
						fmt.Printf("%s %s\n", statusLabel.Render("Disk:"), formatBytes(usage))
					}

					// IP address.
					if eth0, ok := state.Network["eth0"]; ok {
						for _, addr := range eth0.Addresses {
							if addr.Family == "inet" && addr.Scope == "global" {
								fmt.Printf("%s %s\n", statusLabel.Render("IP:"), addr.Address)
								break
							}
						}
					}

					// Uptime.
					if state.Pid > 0 && !inst.LastUsedAt.IsZero() {
						uptime := time.Since(inst.LastUsedAt).Truncate(time.Second)
						fmt.Printf("%s %s\n", statusLabel.Render("Uptime:"), formatDuration(uptime))
					}
				}
			}

			// Snapshots.
			if count := incus.SnapshotCount(server, name); count > 0 {
				fmt.Printf("%s %d\n", statusLabel.Render("Snapshots:"), count)
			}

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
