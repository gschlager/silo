package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/service"
	"github.com/gschlager/silo/internal/svcapi"
	"github.com/spf13/cobra"
)

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage the silo service (secrets cache, container sockets, proxy)",
		Long: `The silo service runs in the background as a systemd user service
(silo.service). It caches 1Password secrets, answers the containers on their
own sockets (notifications, opening URLs, commit signing), proxies
<project>.localhost to the containers, stops idle containers and refreshes
base templates.

silo setup installs and starts it. Logs: journalctl --user -u silo`,
	}
	cmd.AddCommand(newServiceStatusCmd(), newServiceEventsCmd(), newServiceForgetCmd(), newServiceRestartCmd(), newServiceRunCmd())
	return cmd
}

func newServiceRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "run",
		Short:  "Run the service in the foreground (systemd does this)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return service.Run(cmd.Context())
		},
	}
}

func newServiceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the service runs and what it does",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := svcapi.Dial()
			if err != nil {
				color.Warn("The silo service is not running.")
				color.Info("Run `silo setup` to install and start it, or see `journalctl --user -u silo`.")
				return nil
			}
			var st svcapi.StatusInfo
			if err := c.Do(cmd.Context(), "GET", "/v1/status", nil, &st); err != nil {
				return err
			}
			printServiceStatus(st)
			return nil
		},
	}
}

func printServiceStatus(st svcapi.StatusInfo) {
	color.Success("The silo service is running.")
	fmt.Printf("  Version:  %s", st.Version)
	if st.Version != Version {
		fmt.Printf(" (this silo is %s)", Version)
	}
	fmt.Println()
	fmt.Printf("  PID:      %d, up %s\n", st.PID, time.Since(st.Started).Round(time.Second))
	fmt.Printf("  Socket:   %s\n", svcapi.SocketPath())
	fmt.Printf("  Secrets:  %d cached (forgotten on screen lock, suspend and after 12h)\n", st.CachedSecrets)
	if st.Incus {
		fmt.Printf("  Incus:    connected, serving %d containers\n", len(st.Containers))
	} else {
		fmt.Printf("  Incus:    not connected (see journalctl --user -u silo)\n")
	}
	for _, img := range st.BaseRefreshes {
		fmt.Printf("  Rebuilding the base template for %s\n", img)
	}
}

func newServiceForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forget",
		Short: "Forget all cached 1Password secrets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := svcapi.Dial()
			if err != nil {
				color.Info("The silo service is not running, so nothing is cached.")
				return nil
			}
			var res svcapi.ForgetResult
			if err := c.Do(cmd.Context(), "POST", "/v1/secrets/forget", nil, &res); err != nil {
				return err
			}
			color.Success("Forgot %d cached secrets.", res.Count)
			return nil
		},
	}
}

func newServiceRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the service (forgets cached secrets)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if c, err := svcapi.Dial(); err == nil {
				ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
				c.Do(ctx, "POST", "/v1/shutdown", nil, nil)
				cancel()
			}
			// systemd starts it again through silo.socket on the next connection.
			for range 30 {
				time.Sleep(100 * time.Millisecond)
				if _, err := svcapi.Dial(); err == nil {
					color.Success("The silo service restarted.")
					return nil
				}
			}
			return fmt.Errorf("the service didn't come back; see journalctl --user -u silo")
		},
	}
}

// installService installs, enables and starts the silo service's user units.
func installService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	did, err := service.InstallUnits(exe)
	if err != nil {
		return err
	}
	if did == "" {
		color.Info("  The silo service is already installed and running.")
	} else {
		color.Info("  %s.", upperFirst(did))
	}
	return nil
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func newServiceEventsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Follow what the service sees and does (container starts, notifications, ...)",
		Long: `Follow the service's events until interrupted: containers starting and
stopping, idle stops, daemons started by the service, silo-notify messages,
URLs opened from containers, commit signatures and base template refreshes.

With --json, every event is one JSON object per line, for tools.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := svcapi.Dial()
			if err != nil {
				return err
			}
			body, err := c.Stream(cmd.Context(), "/v1/events")
			if err != nil {
				return err
			}
			defer body.Close()
			lines := bufio.NewScanner(body)
			for lines.Scan() {
				if asJSON {
					fmt.Println(lines.Text())
					continue
				}
				var e svcapi.Event
				if json.Unmarshal(lines.Bytes(), &e) != nil {
					continue
				}
				line := e.Time.Local().Format("15:04:05") + "  " + e.Type
				if e.Container != "" {
					line += "  " + e.Container
				}
				if e.Message != "" {
					line += "  " + e.Message
				}
				fmt.Println(line)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print one JSON object per event")
	return cmd
}
