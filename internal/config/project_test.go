package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectConfig_BaseOnly(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, ".silo.yml", `image: ubuntu/22.04
setup:
  - apt install -y git
ports:
  - "8080:80"
`)

	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected config, got nil")
	}
	if cfg.Image != "ubuntu/22.04" {
		t.Errorf("Image = %q, want %q", cfg.Image, "ubuntu/22.04")
	}
	if len(cfg.Setup) != 1 || cfg.Setup[0] != "apt install -y git" {
		t.Errorf("Setup = %v, want [apt install -y git]", cfg.Setup)
	}
}

func TestLoadProjectConfig_UsePreservesOrder(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, ".silo.yml", `use:
  ruby:
    versions: ["3.4"]
  node:
  postgres: { version: 18 }
`)

	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ruby", "node", "postgres"}
	if len(cfg.Use) != len(want) {
		t.Fatalf("Use = %#v, want %v", cfg.Use, want)
	}
	for i, name := range want {
		if cfg.Use[i].Name != name {
			t.Errorf("Use[%d].Name = %q, want %q", i, cfg.Use[i].Name, name)
		}
	}
}

func TestUseListMarshalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := `use:
  ruby:
    versions:
      - "3.4"
  node: null
`
	writeYAML(t, dir, ".silo.yml", src)
	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}

	data, err := MarshalYAML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeYAML(t, dir, ".silo.yml", string(data))
	got, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("re-parse: %v\n%s", err, data)
	}
	if len(got.Use) != 2 || got.Use[0].Name != "ruby" || got.Use[1].Name != "node" {
		t.Errorf("round-trip Use = %#v\nYAML:\n%s", got.Use, data)
	}
}

func TestLoadProjectConfig_LocalOverride(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, ".silo.yml", `image: ubuntu/22.04
setup:
  - apt install -y git
ports:
  - "8080:80"
env:
  FOO: bar
`)
	writeYAML(t, dir, ".silo.local.yml", `image: fedora/43
env:
  FOO: override
`)

	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "fedora/43" {
		t.Errorf("Image = %q, want %q", cfg.Image, "fedora/43")
	}
	// Setup should be preserved from base.
	if len(cfg.Setup) != 1 || cfg.Setup[0] != "apt install -y git" {
		t.Errorf("Setup = %v, want [apt install -y git]", cfg.Setup)
	}
	// Ports should be preserved from base.
	if len(cfg.Ports) != 1 || cfg.Ports[0].Spec != "8080:80" {
		t.Errorf("Ports = %v, want [8080:80]", cfg.Ports)
	}
	// Env should be extended by local, local winning on a collision.
	if cfg.Env["FOO"] != "override" {
		t.Errorf("Env[FOO] = %q, want %q", cfg.Env["FOO"], "override")
	}
}

func TestLoadProjectConfig_LocalExtendsKeyedBlocks(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, ".silo.yml", `image: fedora/43
use:
  ruby:
    version: "3.3"
  node:
setup:
  - bundle install
ports:
  - "8080:80"
  - name: web
    port: 9292:9292
env:
  FOO: bar
  KEEP: base
agents:
  claude:
    mode: console
tools:
  gh:
    credential: {}
daemons:
  redis: redis-server
  pg:
    cmd: postgres
    ports:
      - 5432
`)
	writeYAML(t, dir, ".silo.local.yml", `use:
  ruby:
    version: "3.4"
  valkey:
setup:
  - echo local
ports:
  - "9292:19292"
  - "3000"
env:
  FOO: override
  EXTRA: local
agents:
  codex:
    mode: console
tools:
  npm:
    credential: {}
daemons:
  pg: pg_ctl start
  mailhog: mailhog
`)

	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}

	// use: base order kept, ruby params replaced, valkey appended.
	if len(cfg.Use) != 3 || cfg.Use[0].Name != "ruby" || cfg.Use[1].Name != "node" || cfg.Use[2].Name != "valkey" {
		t.Fatalf("Use names = %v", useNames(cfg.Use))
	}
	var rubyParams struct {
		Version string `yaml:"version"`
	}
	if err := cfg.Use[0].Params.Decode(&rubyParams); err != nil {
		t.Fatal(err)
	}
	if rubyParams.Version != "3.4" {
		t.Errorf("ruby version = %q, want %q", rubyParams.Version, "3.4")
	}

	// Command lists still replace.
	if len(cfg.Setup) != 1 || cfg.Setup[0] != "echo local" {
		t.Errorf("Setup = %v, want [echo local]", cfg.Setup)
	}

	// ports: 8080 kept, 9292 replaced in place (name dropped since local has none), 3000 appended.
	if len(cfg.Ports) != 3 {
		t.Fatalf("Ports = %v, want 3 entries", cfg.Ports)
	}
	if cfg.Ports[0].Spec != "8080:80" || cfg.Ports[1].Spec != "9292:19292" || cfg.Ports[1].Name != "" || cfg.Ports[2].Spec != "3000" {
		t.Errorf("Ports = %v", cfg.Ports)
	}

	// env: extended, local wins.
	if cfg.Env["FOO"] != "override" || cfg.Env["KEEP"] != "base" || cfg.Env["EXTRA"] != "local" {
		t.Errorf("Env = %v", cfg.Env)
	}

	// agents/tools: extended.
	if _, ok := cfg.Agents["claude"]; !ok {
		t.Error("Agents lost claude from base")
	}
	if _, ok := cfg.Agents["codex"]; !ok {
		t.Error("Agents missing codex from local")
	}
	if _, ok := cfg.Tools["gh"]; !ok {
		t.Error("Tools lost gh from base")
	}
	if _, ok := cfg.Tools["npm"]; !ok {
		t.Error("Tools missing npm from local")
	}

	// daemons: redis kept, pg replaced by local, mailhog added.
	if len(cfg.Daemons) != 3 {
		t.Fatalf("Daemons = %v, want 3 entries", cfg.Daemons)
	}
	if cfg.Daemons["redis"].Cmd != "redis-server" {
		t.Errorf("Daemons[redis] = %v", cfg.Daemons["redis"])
	}
	if pg := cfg.Daemons["pg"]; pg.Cmd != "pg_ctl start" || len(pg.Ports) != 0 {
		t.Errorf("Daemons[pg] = %v, want local replacement", pg)
	}
	if cfg.Daemons["mailhog"].Cmd != "mailhog" {
		t.Errorf("Daemons[mailhog] = %v", cfg.Daemons["mailhog"])
	}
}

func TestLoadProjectConfig_LocalDoesNotTouchBaseFile(t *testing.T) {
	// Merging must not mutate the base config's maps/slices in place.
	base := &ProjectConfig{
		Env:     map[string]string{"A": "1"},
		Daemons: map[string]DaemonConfig{"x": {Cmd: "x"}},
		Use:     UseList{{Name: "ruby"}},
		Ports:   []PortForward{{Spec: "80"}},
	}
	local := &ProjectConfig{
		Env:     map[string]string{"B": "2"},
		Daemons: map[string]DaemonConfig{"y": {Cmd: "y"}},
		Use:     UseList{{Name: "node"}},
		Ports:   []PortForward{{Spec: "81"}},
	}
	mergeProjectConfigs(base, local)
	if len(base.Env) != 1 || len(base.Daemons) != 1 || len(base.Use) != 1 || len(base.Ports) != 1 {
		t.Errorf("base mutated: %+v", base)
	}
}

func useNames(u UseList) []string {
	names := make([]string, len(u))
	for i, p := range u {
		names[i] = p.Name
	}
	return names
}

func TestLoadProjectConfig_LocalOnly(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, ".silo.local.yml", `image: fedora/43
setup:
  - dnf install -y git
`)

	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected config, got nil")
	}
	if cfg.Image != "fedora/43" {
		t.Errorf("Image = %q, want %q", cfg.Image, "fedora/43")
	}
}

func TestLoadProjectConfig_NamedPorts(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, ".silo.yml", `image: fedora/43
ports:
  - 5432:15432
  - "9292"
  - name: web
    port: 9292:9292
  - name: ai
    port: 8000
daemons:
  worker:
    cmd: bin/worker
    ports:
      - name: metrics
        port: 9100:9100
`)

	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}

	want := []PortForward{
		{Spec: "5432:15432"},
		{Spec: "9292"},
		{Name: "web", Spec: "9292:9292"},
		{Name: "ai", Spec: "8000"},
	}
	if len(cfg.Ports) != len(want) {
		t.Fatalf("Ports = %#v, want %d entries", cfg.Ports, len(want))
	}
	for i, w := range want {
		if cfg.Ports[i] != w {
			t.Errorf("Ports[%d] = %#v, want %#v", i, cfg.Ports[i], w)
		}
	}

	// Named ports work on daemons too.
	d := cfg.Daemons["worker"]
	if len(d.Ports) != 1 || d.Ports[0] != (PortForward{Name: "metrics", Spec: "9100:9100"}) {
		t.Errorf("worker.Ports = %#v, want [{metrics 9100:9100}]", d.Ports)
	}
}

func TestPortForwardDeviceName(t *testing.T) {
	cases := []struct {
		pf       PortForward
		hostPort int
		want     string
	}{
		{PortForward{Spec: "9292:9292"}, 9292, "port-9292"},
		{PortForward{Spec: "5432:15432"}, 15432, "port-15432"},
		{PortForward{Name: "web", Spec: "9292:9292"}, 9292, "port-web"},
		{PortForward{Name: "AI Service", Spec: "8000"}, 8000, "port-ai-service"},
		{PortForward{Name: "  ", Spec: "8000"}, 8000, "port-8000"}, // blank name falls back
	}
	for _, c := range cases {
		if got := c.pf.DeviceName(c.hostPort); got != c.want {
			t.Errorf("DeviceName(%#v, %d) = %q, want %q", c.pf, c.hostPort, got, c.want)
		}
	}
}

func TestPortForwardMarshalRoundTrip(t *testing.T) {
	cfg := ProjectConfig{
		Ports: []PortForward{{Spec: "5432:15432"}, {Name: "web", Spec: "9292:9292"}},
	}
	data, err := MarshalYAML(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Re-parse the generated YAML to confirm names and specs survive a round trip.
	dir := t.TempDir()
	writeYAML(t, dir, ".silo.yml", string(data))
	got, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("re-parse: %v\n%s", err, data)
	}
	want := []PortForward{{Spec: "5432:15432"}, {Name: "web", Spec: "9292:9292"}}
	if len(got.Ports) != len(want) {
		t.Fatalf("round-trip Ports = %#v\nYAML:\n%s", got.Ports, data)
	}
	for i, w := range want {
		if got.Ports[i] != w {
			t.Errorf("round-trip Ports[%d] = %#v, want %#v\nYAML:\n%s", i, got.Ports[i], w, data)
		}
	}
}

func TestLoadProjectConfig_NoFiles(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Errorf("expected nil, got %+v", cfg)
	}
}

func TestLoadProjectConfigRejectsHostMounts(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, ".silo.yml", "mounts:\n  - ~/.ssh:/host-ssh\n")
	if _, err := LoadProjectConfig(dir); err == nil {
		t.Fatal("expected project-declared host mount to be rejected")
	}
}

func TestLoadProjectConfigRejectsTraversingAgentMode(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, ".silo.yml", "agents:\n  codex:\n    mode: ../../../../.ssh\n")
	if _, err := LoadProjectConfig(dir); err == nil {
		t.Fatal("expected traversing agent mode to be rejected")
	}
}

func TestLoadLocalProjectConfigRejectsHostMounts(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, ".silo.local.yml", "mounts:\n  - ~/secrets:/host-secrets\n")
	if _, err := LoadProjectConfig(dir); err == nil {
		t.Fatal("expected local project-declared host mount to be rejected")
	}
}

func writeYAML(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
