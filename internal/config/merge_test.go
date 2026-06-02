package config

import "testing"

func TestContainerName(t *testing.T) {
	tests := []struct {
		projectDir string
		want       string
	}{
		{"/home/dev/projects/myapp", "silo-myapp"},
		{"/home/dev/my_project", "silo-my-project"},
		{"/home/dev/my.app", "silo-my-app"},
		{"/home/dev/123app", "silo-s123app"},
		{"/home/dev/My App", "silo-My-App"},
	}

	for _, tt := range tests {
		t.Run(tt.projectDir, func(t *testing.T) {
			got := ContainerName(tt.projectDir)
			if got != tt.want {
				t.Errorf("ContainerName(%q) = %q, want %q", tt.projectDir, got, tt.want)
			}
		})
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
		"7000":       {Spec: "7000"},
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
	global := &GlobalConfig{
		Shell:        "zsh",
		User:         "dev",
		DefaultImage: "fedora/43",
		Mounts:       []string{"/host/global:/container/global"},
	}
	project := &ProjectConfig{
		Mounts: []string{"/host/project:/container/project"},
	}

	m := Merge(global, project, "/tmp/test")
	if len(m.Mounts) != 2 {
		t.Fatalf("Mounts = %v, want 2", m.Mounts)
	}
}
