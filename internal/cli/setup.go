package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

const (
	incusUnit          = "incus.service"
	cgroupRelativeLine = "lxc.cgroup.relative = 1"
	defaultMemoryMax   = "80%"
)

func newSetupCmd() *cobra.Command {
	var memoryMax string

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Configure the host so silo containers can't exhaust its memory",
		Long: `Configure the Incus host to keep silo containers from using up all host memory.

This caps the memory of all silo containers together (default ` + defaultMemoryMax + ` of host RAM):

  - Sets lxc.cgroup.relative = 1 on the default Incus profile, so containers are
    created inside the delegated incus.service cgroup instead of at the cgroup root.
  - Sets MemoryMax on incus.service via systemctl, which then bounds the combined
    memory of every container nested under it. A runaway process or a tmpfs fill
    hits this shared limit and gets OOM-killed inside a container, sparing the host.

Setting MemoryMax needs root, so this shells out to "sudo systemctl set-property".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			server, err := incus.Connect()
			if err != nil {
				return err
			}

			// Container cgroup placement (non-privileged).
			color.Status("Configuring container cgroup placement...")
			added, err := incus.EnsureProfileRawLXC(server, "default", cgroupRelativeLine)
			if err != nil {
				return err
			}
			if added {
				color.Info("  Set %q on the default profile.", cgroupRelativeLine)
			} else {
				color.Info("  Default profile already sets lxc.cgroup.relative.")
			}

			// Host memory cap (privileged).
			color.Status("Capping combined container memory on %s...", incusUnit)
			if err := ensureMemoryMax(cmd, memoryMax); err != nil {
				return err
			}

			color.Success("Host setup complete.")
			color.Info("Already-running containers adopt the new cgroup on their next restart")
			color.Info("(silo down && silo up). New containers are placed correctly right away.")
			return nil
		},
	}

	cmd.Flags().StringVar(&memoryMax, "memory-max", defaultMemoryMax, "Combined memory cap for all containers (e.g. 80%, 24GiB)")
	return cmd
}

// ensureMemoryMax sets MemoryMax on incus.service. When the flag was not given
// it only acts if the unit is currently uncapped, so it never overrides a value
// the user tuned by hand. Passing --memory-max always applies the value.
func ensureMemoryMax(cmd *cobra.Command, value string) error {
	current, err := currentMemoryMax()
	if err != nil {
		color.Warn("could not read current MemoryMax (%v); setting it anyway", err)
		current = "infinity"
	}

	if current != "infinity" && !cmd.Flags().Changed("memory-max") {
		color.Info("  Already capped at %s; pass --memory-max to change it.", humanBytes(current))
		return nil
	}

	color.Info("  Running: sudo systemctl set-property %s MemoryMax=%s", incusUnit, value)
	set := exec.Command("sudo", "systemctl", "set-property", incusUnit, "MemoryMax="+value)
	set.Stdin = os.Stdin
	set.Stdout = os.Stderr
	set.Stderr = os.Stderr
	if err := set.Run(); err != nil {
		return fmt.Errorf("setting MemoryMax on %s: %w", incusUnit, err)
	}
	if now, err := currentMemoryMax(); err == nil {
		color.Info("  MemoryMax is now %s.", humanBytes(now))
	}
	return nil
}

// currentMemoryMax returns the MemoryMax of incus.service as reported by
// systemd: a byte count, or "infinity" when uncapped.
func currentMemoryMax() (string, error) {
	out, err := exec.Command("systemctl", "show", incusUnit, "--property=MemoryMax", "--value").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// humanBytes formats a systemd MemoryMax value for display. It passes
// "infinity" through and renders byte counts as GiB.
func humanBytes(v string) string {
	if v == "" || v == "infinity" {
		return v
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return v
	}
	return fmt.Sprintf("%.1f GiB", float64(n)/(1024*1024*1024))
}
