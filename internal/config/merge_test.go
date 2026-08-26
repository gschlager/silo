package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMerge_DefaultSetupDistroMatch(t *testing.T) {
	global := &GlobalConfig{DefaultImage: "fedora/44", DefaultSetup: []string{"dnf install -y git"}}

	// Same distro, different version → default_setup still applies.
	if m := Merge(global, &ProjectConfig{Image: "fedora/43"}, "/tmp/p"); len(m.DefaultSetup) == 0 {
		t.Error("default_setup should run for a same-distro image (fedora/43)")
	}
	// Unset project image → default image → applies.
	if m := Merge(global, &ProjectConfig{}, "/tmp/p"); len(m.DefaultSetup) == 0 {
		t.Error("default_setup should run when the project image is unset")
	}
	// Different distro → skipped (dnf commands wouldn't work).
	if m := Merge(global, &ProjectConfig{Image: "ubuntu/24.04"}, "/tmp/p"); len(m.DefaultSetup) != 0 {
		t.Errorf("default_setup should be skipped for ubuntu, got %v", m.DefaultSetup)
	}
}

func TestMerge_GlobalAgentDisable(t *testing.T) {
	global := &GlobalConfig{Agents: []AgentGlobalConfig{
		{Name: "claude", Enabled: true},
		{Name: "codex", Enabled: false},
	}}
	m := Merge(global, nil, "/tmp/proj")
	if !m.Agents["claude"].Enabled {
		t.Error("claude should be enabled")
	}
	if m.Agents["codex"].Enabled {
		t.Error("codex should be disabled by global config")
	}
}

func TestMerge_ProjectInheritsGlobalDisable(t *testing.T) {
	global := &GlobalConfig{Agents: []AgentGlobalConfig{{Name: "codex", Enabled: false}}}
	// Project mentions codex (e.g. to set a mode) but doesn't touch enabled.
	project := &ProjectConfig{Agents: map[string]AgentProjectConfig{"codex": {Mode: "console"}}}
	m := Merge(global, project, "/tmp/proj")
	if m.Agents["codex"].Enabled {
		t.Error("project mentioning codex without enabled should inherit the global disable")
	}
}

func TestMerge_ProjectReenablesGlobalDisable(t *testing.T) {
	global := &GlobalConfig{Agents: []AgentGlobalConfig{{Name: "codex", Enabled: false}}}
	enabled := true
	project := &ProjectConfig{Agents: map[string]AgentProjectConfig{"codex": {Enabled: &enabled}}}
	m := Merge(global, project, "/tmp/proj")
	if !m.Agents["codex"].Enabled {
		t.Error("a project should be able to re-enable a globally disabled agent")
	}
}

func TestContainerName(t *testing.T) {
	tests := []struct {
		projectDir string
		want       string
	}{
		{"/home/dev/projects/myapp", "silo-myapp-"},
		{"/home/dev/my_project", "silo-my-project-"},
		{"/home/dev/my.app", "silo-my-app-"},
		{"/home/dev/123app", "silo-s123app-"},
		{"/home/dev/My App", "silo-My-App-"},
	}

	for _, tt := range tests {
		t.Run(tt.projectDir, func(t *testing.T) {
			got := ContainerName(tt.projectDir)
			if !strings.HasPrefix(got, tt.want) || len(got) != len(tt.want)+16 {
				t.Errorf("ContainerName(%q) = %q, want prefix %q plus 16 hex characters", tt.projectDir, got, tt.want)
			}
		})
	}
}

func TestContainerNameSeparatesSameBasename(t *testing.T) {
	a := ContainerName("/work/customer/app")
	b := ContainerName("/tmp/untrusted/app")
	if a == b {
		t.Fatalf("same-basename projects collided: %q", a)
	}
}

func TestContainerNameCanonicalizesSymlinks(t *testing.T) {
	realDir := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	if realName, linkName := ContainerName(realDir), ContainerName(link); realName != linkName {
		t.Fatalf("canonical project aliases got different names: %q and %q", realName, linkName)
	}
}

func TestContainerNameFitsIncusLimit(t *testing.T) {
	name := ContainerName("/tmp/" + strings.Repeat("a", 100))
	if len(name) > 63 {
		t.Fatalf("container name has %d characters, want at most 63: %q", len(name), name)
	}
}

func TestMerge_ImageOverride(t *testing.T) {
	global := &GlobalConfig{
		DefaultImage: "fedora/43",
		Shell:        "zsh",
		User:         "dev",
	}

	t.Run("no project", func(t *testing.T) {
		m := Merge(global, nil, "/tmp/test")
		if m.Image != "fedora/43" {
			t.Errorf("Image = %q, want fedora/43", m.Image)
		}
		if len(m.DefaultSetup) != 0 {
			t.Errorf("DefaultSetup should be empty when global has none")
		}
	})

	t.Run("project overrides image", func(t *testing.T) {
		project := &ProjectConfig{Image: "debian/bookworm"}
		m := Merge(global, project, "/tmp/test")
		if m.Image != "debian/bookworm" {
			t.Errorf("Image = %q, want debian/bookworm", m.Image)
		}
	})

	t.Run("project with default image gets default setup", func(t *testing.T) {
		g := &GlobalConfig{
			DefaultImage: "fedora/43",
			DefaultSetup: []string{"dnf install -y git"},
			Shell:        "zsh",
			User:         "dev",
		}
		project := &ProjectConfig{Image: "fedora/43"}
		m := Merge(g, project, "/tmp/test")
		if len(m.DefaultSetup) != 1 {
			t.Errorf("DefaultSetup = %v, want [dnf install -y git]", m.DefaultSetup)
		}
	})
}

func TestMerge_DaemonPorts(t *testing.T) {
	global := &GlobalConfig{Shell: "zsh", User: "dev", DefaultImage: "fedora/43"}
	project := &ProjectConfig{
		Ports: []PortForward{{Spec: "5432:15432"}},
		Daemons: map[string]DaemonConfig{
			"rails": {Cmd: "bin/rails s", Ports: []PortForward{{Spec: "3000"}}},
			"ember": {Cmd: "bin/ember-cli", Ports: []PortForward{{Spec: "4200"}}},
		},
	}

	m := Merge(global, project, "/tmp/test")

	// Should have top-level port + daemon ports.
	if len(m.Ports) != 3 {
		t.Fatalf("Ports = %v, want 3 ports", m.Ports)
	}
	// First should be the top-level port.
	if m.Ports[0].Spec != "5432:15432" {
		t.Errorf("Ports[0] = %q, want 5432:15432", m.Ports[0].Spec)
	}
}

// TestMerge_DaemonPortsDeduped ensures a port declared both at the top level and
// on a daemon is only forwarded once, even when the spellings differ (e.g.
// "9292:9292" vs the shorthand "9292", or a different host mapping for the same
// container port). Forwarding it twice would try to bind the host port again.
func TestMerge_DaemonPortsDeduped(t *testing.T) {
	global := &GlobalConfig{Shell: "zsh", User: "dev", DefaultImage: "fedora/43"}
	project := &ProjectConfig{
		Ports: []PortForward{{Name: "web", Spec: "9292:9292"}, {Spec: "8000:18000"}},
		Daemons: map[string]DaemonConfig{
			"web":    {Cmd: "bin/dev web", Ports: []PortForward{{Spec: "9292"}}},
			"ai":     {Cmd: "bin/dev ai", Ports: []PortForward{{Spec: "8000"}}},
			"worker": {Cmd: "bin/dev worker", Ports: []PortForward{{Spec: "7000"}}},
		},
	}

	m := Merge(global, project, "/tmp/test")

	// The two overlapping daemon ports (9292, 8000) are dropped; only the new
	// worker port (7000) is added to the two top-level ports. The top-level
	// entries win, so the "web" forward keeps its name and 9292:9292 mapping.
	want := map[string]PortForward{
		"9292:9292":  {Name: "web", Spec: "9292:9292"},
		"8000:18000": {Spec: "8000:18000"},
		"7000":       {Name: "worker", Spec: "7000"}, // labeled with its daemon
	}
	if len(m.Ports) != len(want) {
		t.Fatalf("Ports = %v, want %d entries", m.Ports, len(want))
	}
	for _, p := range m.Ports {
		w, ok := want[p.Spec]
		if !ok {
			t.Errorf("unexpected port %#v in %v", p, m.Ports)
			continue
		}
		if p != w {
			t.Errorf("port %q = %#v, want %#v", p.Spec, p, w)
		}
	}
}

func TestMerge_Mounts(t *testing.T) {
	projectDir := "/tmp/test"
	projectKey := ProjectName(projectDir)
	global := &GlobalConfig{
		Shell:        "zsh",
		User:         "dev",
		DefaultImage: "fedora/43",
		Mounts:       []string{"/host/global:/container/global"},
		ProjectMounts: map[string][]string{
			projectKey: {"/host/scoped:/container/scoped"},
		},
	}
	project := &ProjectConfig{
		Mounts: []string{"/host/project:/container/project"},
	}

	m := Merge(global, project, projectDir)
	if len(m.Mounts) != 2 || m.Mounts[0] != global.Mounts[0] || m.Mounts[1] != global.ProjectMounts[projectKey][0] {
		t.Fatalf("Mounts = %v, want host-owned global and project-scoped mounts", m.Mounts)
	}
}

func TestMerge_CarriesModeEnv(t *testing.T) {
	global := &GlobalConfig{Agents: []AgentGlobalConfig{{
		Name:    "claude",
		Enabled: true,
		Mode:    "oauth",
		Modes: map[string]ModeConfig{
			"bedrock": {Env: map[string]string{
				"CLAUDE_CODE_USE_BEDROCK": "1",
				"AWS_REGION":              "us-west-2",
			}},
		},
	}}}

	// No project override: modes carry through from global.
	m := Merge(global, nil, "/tmp/proj")
	if got := m.Agents["claude"].Modes["bedrock"].Env["AWS_REGION"]; got != "us-west-2" {
		t.Fatalf("mode env not carried: got %q", got)
	}

	// A project that only switches the mode still inherits the global modes block.
	project := &ProjectConfig{Agents: map[string]AgentProjectConfig{"claude": {Mode: "bedrock"}}}
	m = Merge(global, project, "/tmp/proj")
	agent := m.Agents["claude"]
	if agent.Mode != "bedrock" {
		t.Fatalf("mode = %q, want bedrock", agent.Mode)
	}
	keys := agent.ModeEnvKeys()
	if len(keys) != 2 || keys[0] != "AWS_REGION" || keys[1] != "CLAUDE_CODE_USE_BEDROCK" {
		t.Fatalf("ModeEnvKeys = %v, want sorted [AWS_REGION CLAUDE_CODE_USE_BEDROCK]", keys)
	}
}

// useList builds a UseList without going through YAML, for tests that only care
// about which presets are named.
func useList(names ...string) UseList {
	var u UseList
	for _, n := range names {
		u = append(u, PresetUse{Name: n})
	}
	return u
}

func TestMerge_UserPresetContributesDaemonsAndEnv(t *testing.T) {
	global := &GlobalConfig{Presets: map[string]UserPreset{
		"segno": {
			Env: map[string]string{"SEGNO_HOME": "/opt/segno"},
			Daemons: map[string]DaemonConfig{
				"segno-claude": {Cmd: "segno-runner claude", Autostart: true},
				"segno-codex":  {Cmd: "segno-runner codex", Autostart: true},
			},
		},
	}}

	// Opted in: the preset's daemons and env show up.
	m := Merge(global, &ProjectConfig{Use: useList("segno")}, "/tmp/proj")
	if len(m.Daemons) != 2 {
		t.Errorf("expected 2 daemons from the preset, got %v", m.Daemons)
	}
	if m.Env["SEGNO_HOME"] != "/opt/segno" {
		t.Errorf("expected the preset's env, got %v", m.Env)
	}

	// Not opted in: nothing from the preset runs. This is the whole point of
	// use: over a global daemons: block.
	m = Merge(global, &ProjectConfig{}, "/tmp/proj")
	if len(m.Daemons) != 0 {
		t.Errorf("preset daemons leaked into a project that didn't opt in: %v", m.Daemons)
	}
	if len(m.Env) != 0 {
		t.Errorf("preset env leaked into a project that didn't opt in: %v", m.Env)
	}
}

func TestMerge_ProjectOverridesUserPreset(t *testing.T) {
	global := &GlobalConfig{Presets: map[string]UserPreset{
		"segno": {
			Env:     map[string]string{"SEGNO_HOME": "/opt/segno", "KEEP": "yes"},
			Daemons: map[string]DaemonConfig{"segno-codex": {Cmd: "segno-runner codex"}},
		},
	}}
	project := &ProjectConfig{
		Use:     useList("segno"),
		Env:     map[string]string{"SEGNO_HOME": "/srv/segno"},
		Daemons: map[string]DaemonConfig{"segno-codex": {Cmd: "segno-runner codex --debug"}},
	}

	m := Merge(global, project, "/tmp/proj")
	if m.Env["SEGNO_HOME"] != "/srv/segno" {
		t.Errorf("project env should win, got %q", m.Env["SEGNO_HOME"])
	}
	if m.Env["KEEP"] != "yes" {
		t.Error("preset env keys the project doesn't set should survive")
	}
	if got := m.Daemons["segno-codex"].Cmd; got != "segno-runner codex --debug" {
		t.Errorf("project daemon should win, got %q", got)
	}
}

func TestMerge_UserPresetDaemonPortsAreForwarded(t *testing.T) {
	global := &GlobalConfig{Presets: map[string]UserPreset{
		"segno": {Daemons: map[string]DaemonConfig{
			"segno-web": {Cmd: "segno-runner web", Ports: []PortForward{{Spec: "4000:14000"}}},
		}},
	}}
	m := Merge(global, &ProjectConfig{Use: useList("segno")}, "/tmp/proj")

	var found bool
	for _, p := range m.Ports {
		if p.Spec == "4000:14000" && p.Name == "segno-web" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the preset daemon's port to be forwarded and named, got %v", m.Ports)
	}
}

// A port declared by two daemons is forwarded once; which daemon names it must
// not depend on map iteration order.
func TestMerge_DaemonPortNamingIsStable(t *testing.T) {
	project := &ProjectConfig{Daemons: map[string]DaemonConfig{
		"alpha": {Cmd: "a", Ports: []PortForward{{Spec: "3000:13000"}}},
		"zulu":  {Cmd: "z", Ports: []PortForward{{Spec: "3000:13000"}}},
	}}
	for i := 0; i < 20; i++ {
		m := Merge(&GlobalConfig{}, project, "/tmp/proj")
		if len(m.Ports) != 1 {
			t.Fatalf("expected the shared port forwarded once, got %v", m.Ports)
		}
		if m.Ports[0].Name != "alpha" {
			t.Fatalf("expected 'alpha' to claim the port every run, got %q", m.Ports[0].Name)
		}
	}
}
