package incus

import (
	"fmt"
	"os/exec"

	"github.com/lxc/incus/v7/shared/api"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v3"
)

const (
	// BridgeName is the network silo creates on a fresh Incus install.
	BridgeName = "incusbr0"

	// DefaultPoolPath is where Incus keeps the default storage pool. On btrfs
	// it's created as a subvolume there instead of a loop file.
	DefaultPoolPath = "/var/lib/incus/storage-pools/default"
)

// StorageDriverFor returns "btrfs" when path lives on a btrfs filesystem (the
// Fedora default) and the btrfs tool Incus needs for it is installed, and
// "dir" otherwise.
func StorageDriverFor(path string) string {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil || st.Type != unix.BTRFS_SUPER_MAGIC {
		return "dir"
	}
	if _, err := exec.LookPath("btrfs"); err != nil {
		return "dir"
	}
	return "btrfs"
}

// DefaultPreseed returns the `incus admin init --preseed` YAML that silo uses
// on a fresh Incus install: a NAT bridge, a default storage pool with the
// given driver, and a default profile that uses both. rawLXC is set on the
// default profile (silo uses it for lxc.cgroup.relative, see `silo setup`).
func DefaultPreseed(driver, rawLXC string) (string, error) {
	poolConfig := map[string]string{}
	if driver == "btrfs" {
		poolConfig["source"] = DefaultPoolPath
	}

	profileConfig := map[string]string{}
	if rawLXC != "" {
		profileConfig["raw.lxc"] = rawLXC
	}

	preseed := api.InitLocalPreseed{
		Networks: []api.InitNetworksProjectPost{{
			NetworksPost: api.NetworksPost{
				Name: BridgeName,
				Type: "bridge",
				NetworkPut: api.NetworkPut{Config: map[string]string{
					"ipv4.address": "auto",
					"ipv4.nat":     "true",
					"ipv6.address": "auto",
					"ipv6.nat":     "true",
				}},
			},
			Project: "default",
		}},
		StoragePools: []api.StoragePoolsPost{{
			Name:           "default",
			Driver:         driver,
			StoragePoolPut: api.StoragePoolPut{Config: poolConfig},
		}},
		Profiles: []api.InitProfileProjectPost{{
			ProfilesPost: api.ProfilesPost{
				Name: "default",
				ProfilePut: api.ProfilePut{
					Description: "Default Incus profile",
					Config:      profileConfig,
					Devices: map[string]map[string]string{
						"eth0": {"name": "eth0", "network": BridgeName, "type": "nic"},
						"root": {"path": "/", "pool": "default", "type": "disk"},
					},
				},
			},
		}},
	}

	out, err := yaml.Marshal(preseed)
	if err != nil {
		return "", fmt.Errorf("building Incus preseed: %w", err)
	}
	return string(out), nil
}
