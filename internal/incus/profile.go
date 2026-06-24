package incus

import (
	"fmt"
	"strings"

	incuscli "github.com/lxc/incus/v6/client"
)

// EnsureProfileRawLXC appends a single raw.lxc line to a profile's existing
// raw.lxc config, unless a line mentioning the same lxc key is already present.
// It returns true when the profile was modified.
//
// silo uses this to set "lxc.cgroup.relative = 1" on the default profile so
// containers are created inside the delegated incus.service cgroup rather than
// at the cgroup root — which lets a MemoryMax cap on incus.service bound the
// memory of all silo containers together.
func EnsureProfileRawLXC(server incuscli.InstanceServer, profile, line string) (bool, error) {
	prof, etag, err := server.GetProfile(profile)
	if err != nil {
		return false, fmt.Errorf("getting profile %q: %w", profile, err)
	}

	key := lxcKey(line)
	existing := prof.Config["raw.lxc"]
	for _, l := range strings.Split(existing, "\n") {
		if key != "" && lxcKey(l) == key {
			return false, nil
		}
	}

	updated := existing
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += line

	put := prof.Writable()
	if put.Config == nil {
		put.Config = map[string]string{}
	}
	put.Config["raw.lxc"] = updated

	if err := server.UpdateProfile(profile, put, etag); err != nil {
		return false, fmt.Errorf("updating profile %q: %w", profile, err)
	}
	return true, nil
}

// lxcKey returns the config key of a raw.lxc line ("lxc.cgroup.relative = 1"
// -> "lxc.cgroup.relative"), or "" if the line has no key.
func lxcKey(line string) string {
	k, _, ok := strings.Cut(line, "=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(k)
}
