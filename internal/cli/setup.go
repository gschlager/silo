package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"slices"
	"strconv"
	"strings"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

const (
	incusUnit          = "incus.service"
	incusSocket        = "incus.socket"
	incusAdminGroup    = "incus-admin"
	cgroupRelativeLine = "lxc.cgroup.relative = 1"
	defaultMemoryMax   = "80%"
)

func newSetupCmd() *cobra.Command {
	var memoryMax string

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Prepare the host for silo (Incus access, init, firewall, memory cap)",
		Long: `Prepare the Incus host for silo. Safe to run again, every step checks first.

  - Adds you to the ` + incusAdminGroup + ` group, so silo can talk to Incus.
  - Initializes Incus if it has no storage pool yet: a NAT bridge (` + incus.BridgeName + `),
    a default storage pool (a btrfs subvolume on btrfs, a directory otherwise)
    and a default profile using both. An existing setup is never changed.
  - Puts ` + incus.BridgeName + ` into firewalld's trusted zone, so containers get network
    access (only when firewalld is running).
  - Caps the memory of all silo containers together (default ` + defaultMemoryMax + ` of host RAM):
    sets lxc.cgroup.relative = 1 on the default profile, so containers are
    created inside the delegated incus.service cgroup, and sets MemoryMax on
    incus.service. A runaway process or a tmpfs fill hits this shared limit
    and gets OOM-killed inside a container, sparing the host.
  - Installs and starts the silo service (a systemd user service), see
    ` + "`silo service`" + `.

Steps that need root shell out to sudo.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			color.Status("Checking access to Incus...")
			needsRelogin, err := ensureIncusAdmin()
			if err != nil {
				return err
			}

			color.Status("Checking Incus initialization...")
			if err := ensureIncusSocket(); err != nil {
				return err
			}
			if err := ensureIncusInitialized(); err != nil {
				return err
			}

			color.Status("Checking firewall...")
			if err := ensureFirewall(); err != nil {
				return err
			}

			// Host memory cap (privileged).
			color.Status("Capping combined container memory on %s...", incusUnit)
			if err := ensureMemoryMax(cmd, memoryMax); err != nil {
				return err
			}

			color.Status("Checking the silo service...")
			if err := installService(); err != nil {
				return fmt.Errorf("installing the silo service: %w", err)
			}

			// Container cgroup placement (needs API access).
			color.Status("Configuring container cgroup placement...")
			server, err := incus.Connect()
			if err != nil {
				if needsRelogin {
					color.Warn("You're in %s, but it only takes effect after you log out and back in.", incusAdminGroup)
					color.Info("Run silo setup again after that to finish the last step.")
					return nil
				}
				return err
			}
			added, err := incus.EnsureProfileRawLXC(server, "default", cgroupRelativeLine)
			if err != nil {
				return err
			}
			if added {
				color.Info("  Set %q on the default profile.", cgroupRelativeLine)
			} else {
				color.Info("  Default profile already sets lxc.cgroup.relative.")
			}

			color.Success("Host setup complete.")
			if needsRelogin {
				color.Info("Log out and back in so the %s group takes effect.", incusAdminGroup)
			}
			color.Info("Already-running containers adopt the new cgroup on their next restart")
			color.Info("(silo down && silo up). New containers are placed correctly right away.")
			return nil
		},
	}

	cmd.Flags().StringVar(&memoryMax, "memory-max", defaultMemoryMax, "Combined memory cap for all containers (e.g. 80%, 24GiB)")
	return cmd
}

// ensureIncusAdmin adds the current user to the incus-admin group unless they
// are already a member. It returns true when the group isn't active in this
// session yet (just added, or added earlier without logging in again), because
// a new group only applies to sessions started after that.
func ensureIncusAdmin() (bool, error) {
	if os.Geteuid() == 0 {
		color.Info("  Running as root, no group needed.")
		return false, nil
	}

	group, err := user.LookupGroup(incusAdminGroup)
	if err != nil {
		return false, fmt.Errorf("group %s not found, is Incus installed? (%w)", incusAdminGroup, err)
	}
	me, err := user.Current()
	if err != nil {
		return false, err
	}
	groups, err := me.GroupIds()
	if err != nil {
		return false, fmt.Errorf("reading groups of %s: %w", me.Username, err)
	}
	if slices.Contains(groups, group.Gid) {
		color.Info("  %s is already in %s.", me.Username, incusAdminGroup)
	} else if err := runSudo("", "usermod", "-aG", incusAdminGroup, me.Username); err != nil {
		return false, fmt.Errorf("adding %s to %s: %w", me.Username, incusAdminGroup, err)
	}

	return !groupActive(group.Gid), nil
}

// groupActive reports whether gid is one of this process's groups.
func groupActive(gid string) bool {
	if strconv.Itoa(os.Getegid()) == gid {
		return true
	}
	active, err := os.Getgroups()
	if err != nil {
		return false
	}
	return slices.ContainsFunc(active, func(g int) bool { return strconv.Itoa(g) == gid })
}

// ensureIncusSocket makes sure incus.socket is enabled and listening. The RPM
// only enables it (through its preset), so right after installing it's not
// running until the next reboot.
func ensureIncusSocket() error {
	if exec.Command("systemctl", "is-active", "--quiet", incusSocket).Run() == nil {
		return nil
	}
	if err := runSudo("", "systemctl", "enable", "--now", incusSocket); err != nil {
		return fmt.Errorf("starting %s: %w", incusSocket, err)
	}
	return nil
}

// ensureIncusInitialized runs `incus admin init` with silo's default preseed
// when Incus has no storage pool yet. Anything already set up is left alone.
func ensureIncusInitialized() error {
	pools, err := sudoOutput("incus", "storage", "list", "--format", "csv")
	if err != nil {
		return fmt.Errorf("listing Incus storage pools: %w", err)
	}
	if pools != "" {
		color.Info("  Incus is already initialized.")
		return nil
	}

	driver := incus.StorageDriverFor("/var/lib/incus")
	preseed, err := incus.DefaultPreseed(driver, cgroupRelativeLine)
	if err != nil {
		return err
	}
	color.Info("  Initializing Incus (%s storage pool, %s bridge).", driver, incus.BridgeName)
	if err := runSudo(preseed, "incus", "admin", "init", "--preseed"); err != nil {
		return fmt.Errorf("initializing Incus: %w", err)
	}
	return nil
}

// ensureFirewall puts the Incus bridge into firewalld's trusted zone, so
// containers can reach DHCP, DNS and the outside world.
func ensureFirewall() error {
	if exec.Command("systemctl", "is-active", "--quiet", "firewalld").Run() != nil {
		color.Info("  firewalld isn't running, nothing to do.")
		return nil
	}

	// Exits non-zero when the interface has no zone yet, that's fine.
	zone, _ := sudoOutput("firewall-cmd", "--permanent", "--get-zone-of-interface="+incus.BridgeName)
	if zone == "trusted" {
		color.Info("  %s is already in the trusted zone.", incus.BridgeName)
		return nil
	}

	if err := runSudo("", "firewall-cmd", "--permanent", "--zone=trusted", "--change-interface="+incus.BridgeName); err != nil {
		return fmt.Errorf("adding %s to the trusted zone: %w", incus.BridgeName, err)
	}
	if err := runSudo("", "firewall-cmd", "--reload"); err != nil {
		return fmt.Errorf("reloading firewalld: %w", err)
	}
	return nil
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

	if err := runSudo("", "systemctl", "set-property", incusUnit, "MemoryMax="+value); err != nil {
		return fmt.Errorf("setting MemoryMax on %s: %w", incusUnit, err)
	}
	if now, err := currentMemoryMax(); err == nil {
		color.Info("  MemoryMax is now %s.", humanBytes(now))
	}
	return nil
}

// runSudo runs a command with sudo and shows its output. When input is not
// empty it's passed on stdin; sudo itself asks for the password on the tty.
func runSudo(input string, args ...string) error {
	color.Info("  Running: sudo %s", strings.Join(args, " "))
	c := exec.Command("sudo", args...)
	if input != "" {
		c.Stdin = strings.NewReader(input)
	} else {
		c.Stdin = os.Stdin
	}
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr
	return c.Run()
}

// sudoOutput runs a command with sudo and returns its trimmed stdout. On
// failure the error includes what the command printed on stderr.
func sudoOutput(args ...string) (string, error) {
	c := exec.Command("sudo", args...)
	c.Stdin = os.Stdin
	out, err := c.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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
