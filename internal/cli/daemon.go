package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage daemons running inside the container",
	}

	cmd.AddCommand(
		newDaemonListCmd(),
		newDaemonStartCmd(),
		newDaemonStopCmd(),
		newDaemonRestartCmd(),
		newDaemonLogsCmd(),
	)
	return cmd
}

func newDaemonListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured daemons and their state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			if len(cfg.Daemons) == 0 {
				fmt.Println("No daemons configured.")
				return nil
			}

			names := make([]string, 0, len(cfg.Daemons))
			for name := range cfg.Daemons {
				names = append(names, name)
			}
			sort.Strings(names)

			server, err := incus.Connect()
			if err != nil {
				return err
			}
			running := incus.Exists(server, cfg.ContainerName) &&
				incus.IsRunning(server, cfg.ContainerName)

			for _, name := range names {
				dcfg := cfg.Daemons[name]
				state := "stopped"
				if running {
					out, err := incus.Exec(ctx, server, cfg.ContainerName, incus.ExecOpts{}, []string{
						"su", "-", cfg.User, "-c",
						fmt.Sprintf("systemctl --user is-active silo-%s 2>/dev/null || true", name),
					})
					if err == nil {
						switch strings.TrimSpace(out) {
						case "active":
							state = "running"
						case "failed":
							state = "failed"
						}
					}
				}
				extra := ""
				if !dcfg.Autostart {
					extra = "  (manual)"
				}
				fmt.Printf("  %-16s %s%s\n", name, state, extra)
			}
			return nil
		},
	}
}

func newDaemonStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "start <daemon>",
		Short:             "Start a daemon",
		Args:              requireArgs(1, "daemon name"),
		ValidArgsFunction: completeDaemonNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonUnitAction(cmd, args[0], "start")
		},
	}
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "stop <daemon>",
		Short:             "Stop a running daemon",
		Args:              requireArgs(1, "daemon name"),
		ValidArgsFunction: completeDaemonNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonUnitAction(cmd, args[0], "stop")
		},
	}
}

func newDaemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "restart <daemon>",
		Short:             "Restart a daemon",
		Args:              requireArgs(1, "daemon name"),
		ValidArgsFunction: completeDaemonNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonUnitAction(cmd, args[0], "restart")
		},
	}
}

func newDaemonLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs [daemon]",
		Short: "Tail logs for a specific daemon or all daemons",
		Long: `Without arguments, tails all daemon logs interleaved.
With a daemon name, tails logs for that specific daemon.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeDaemonNames,
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

			if err := ensureRunning(ctx, server, cfg.ContainerName); err != nil {
				return err
			}

			var journalCmd string
			if len(args) == 0 {
				journalCmd = "journalctl --user -u 'silo-*' -f"
			} else {
				daemon := args[0]
				if _, ok := cfg.Daemons[daemon]; !ok {
					return fmt.Errorf("unknown daemon %q", daemon)
				}
				// If the daemon isn't running, dropping -f shows the past
				// logs and exits instead of hanging forever waiting for
				// new entries that will never come.
				state, _ := incus.Exec(ctx, server, cfg.ContainerName, incus.ExecOpts{}, []string{
					"su", "-", cfg.User, "-c",
					fmt.Sprintf("systemctl --user is-active silo-%s 2>/dev/null || true", daemon),
				})
				if strings.TrimSpace(state) == "active" {
					journalCmd = fmt.Sprintf("journalctl --user -u silo-%s -f", daemon)
				} else {
					color.Info("Daemon %q is not running; showing past logs.", daemon)
					journalCmd = fmt.Sprintf("journalctl --user -u silo-%s --no-pager", daemon)
				}
			}

			opts := incus.UserOpts(cfg.UserHome(), "")
			opts.Env = cfg.HostEnv()
			return incus.ExecInteractive(ctx, server, cfg.ContainerName, opts,
				cfg.LoginCmd(journalCmd))
		},
	}
}

// runDaemonUnitAction is shared by start/stop/restart — they differ only in the
// systemctl verb and whether a stopped container counts as an error.
func runDaemonUnitAction(cmd *cobra.Command, daemon, action string) error {
	ctx := cmd.Context()

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	server, err := incus.Connect()
	if err != nil {
		return err
	}

	// stop only makes sense against a running container; start/restart can
	// bring it up first.
	if action == "stop" {
		if err := requireRunning(server, cfg.ContainerName); err != nil {
			return err
		}
	} else {
		if err := ensureRunning(ctx, server, cfg.ContainerName); err != nil {
			return err
		}
	}

	if _, ok := cfg.Daemons[daemon]; !ok {
		return fmt.Errorf("unknown daemon %q", daemon)
	}

	_, err = incus.Exec(ctx, server, cfg.ContainerName, incus.ExecOpts{}, []string{
		"su", "-", cfg.User, "-c",
		fmt.Sprintf("systemctl --user %s silo-%s", action, daemon),
	})
	if err != nil && (action == "start" || action == "restart") {
		// Most likely cause is the daemon's own command exiting non-zero;
		// dump the last few journal lines so the user sees why.
		tail, _ := incus.Exec(ctx, server, cfg.ContainerName, incus.ExecOpts{}, []string{
			"su", "-", cfg.User, "-c",
			fmt.Sprintf("journalctl --user -u silo-%s -n 10 --no-pager 2>/dev/null || true", daemon),
		})
		if tail != "" {
			fmt.Fprintln(os.Stderr, strings.TrimRight(tail, "\n"))
		}
	}
	return err
}
