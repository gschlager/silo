package incus

import (
	"testing"

	"github.com/lxc/incus/v7/shared/api"
	"gopkg.in/yaml.v3"
)

func parsePreseed(t *testing.T, driver, rawLXC string) api.InitLocalPreseed {
	t.Helper()
	out, err := DefaultPreseed(driver, rawLXC)
	if err != nil {
		t.Fatal(err)
	}
	var p api.InitLocalPreseed
	if err := yaml.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("preseed is not valid YAML: %v\n%s", err, out)
	}
	return p
}

func TestDefaultPreseedBtrfs(t *testing.T) {
	p := parsePreseed(t, "btrfs", "lxc.cgroup.relative = 1")

	if len(p.StoragePools) != 1 {
		t.Fatalf("want 1 storage pool, got %d", len(p.StoragePools))
	}
	pool := p.StoragePools[0]
	if pool.Name != "default" || pool.Driver != "btrfs" || pool.Config["source"] != DefaultPoolPath {
		t.Errorf("unexpected pool: %+v", pool)
	}

	if len(p.Networks) != 1 || p.Networks[0].Name != BridgeName || p.Networks[0].Type != "bridge" {
		t.Fatalf("unexpected networks: %+v", p.Networks)
	}

	if len(p.Profiles) != 1 {
		t.Fatalf("want 1 profile, got %d", len(p.Profiles))
	}
	prof := p.Profiles[0]
	if prof.Name != "default" {
		t.Errorf("profile name = %q, want default", prof.Name)
	}
	if got := prof.Config["raw.lxc"]; got != "lxc.cgroup.relative = 1" {
		t.Errorf("raw.lxc = %q", got)
	}
	if prof.Devices["eth0"]["network"] != BridgeName {
		t.Errorf("eth0 not on %s: %+v", BridgeName, prof.Devices["eth0"])
	}
	if prof.Devices["root"]["pool"] != "default" || prof.Devices["root"]["path"] != "/" {
		t.Errorf("unexpected root disk: %+v", prof.Devices["root"])
	}
}

func TestDefaultPreseedDirHasNoSource(t *testing.T) {
	p := parsePreseed(t, "dir", "")

	pool := p.StoragePools[0]
	if pool.Driver != "dir" {
		t.Errorf("driver = %q, want dir", pool.Driver)
	}
	if _, ok := pool.Config["source"]; ok {
		t.Errorf("dir pool should let Incus pick the path, got source %q", pool.Config["source"])
	}
	if _, ok := p.Profiles[0].Config["raw.lxc"]; ok {
		t.Errorf("raw.lxc should be unset when empty")
	}
}
