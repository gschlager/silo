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
		m := Merge(global, nil, nil, "/tmp/test")
		if m.Image != "fedora/43" {
			t.Errorf("Image = %q, want fedora/43", m.Image)
		}
		if len(m.DefaultSetup) != 0 {
			t.Errorf("DefaultSetup should be empty when global has none")
		}
	})

	t.Run("project overrides image", func(t *testing.T) {
		project := &ProjectConfig{Image: "debian/bookworm"}
		m := Merge(global, project, nil, "/tmp/test")
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
		m := Merge(g, project, nil, "/tmp/test")
		if len(m.DefaultSetup) != 1 {
			t.Errorf("DefaultSetup = %v, want [dnf install -y git]", m.DefaultSetup)
		}
	})
}

func TestMerge_DaemonPorts(t *testing.T) {
	global := &GlobalConfig{Shell: "zsh", User: "dev", DefaultImage: "fedora/43"}
	project := &ProjectConfig{
		Ports: []string{"5432:15432"},
		Daemons: map[string]DaemonConfig{
			"rails": {Cmd: "bin/rails s", Ports: []string{"3000"}},
			"ember": {Cmd: "bin/ember-cli", Ports: []string{"4200"}},
		},
	}

	m := Merge(global, project, nil, "/tmp/test")

	// Should have top-level port + daemon ports.
	if len(m.Ports) != 3 {
		t.Fatalf("Ports = %v, want 3 ports", m.Ports)
	}
	// First should be the top-level port.
	if m.Ports[0] != "5432:15432" {
		t.Errorf("Ports[0] = %q, want 5432:15432", m.Ports[0])
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

	m := Merge(global, project, nil, "/tmp/test")
	if len(m.Mounts) != 2 {
		t.Fatalf("Mounts = %v, want 2", m.Mounts)
	}
}

func TestMerge_GitCredentialFromContainer(t *testing.T) {
	global := &GlobalConfig{Shell: "zsh", User: "dev", DefaultImage: "fedora/43"}
	container := &ContainerConfig{
		GitCredential: &CredentialConfig{
			Source: "1password",
			Ref:    "op://Vault/Item/token",
		},
	}

	m := Merge(global, nil, container, "/tmp/test")
	if m.GitCredential == nil {
		t.Fatal("expected GitCredential from container config")
	}
	if m.GitCredential.Source != "1password" {
		t.Errorf("GitCredential.Source = %q, want %q", m.GitCredential.Source, "1password")
	}
	if m.GitCredential.Ref != "op://Vault/Item/token" {
		t.Errorf("GitCredential.Ref = %q, want %q", m.GitCredential.Ref, "op://Vault/Item/token")
	}
}

func TestMerge_ProjectGitCredentialIgnored(t *testing.T) {
	global := &GlobalConfig{Shell: "zsh", User: "dev", DefaultImage: "fedora/43"}
	project := &ProjectConfig{
		Git: GitConfig{
			Credential: &CredentialConfig{
				Source: "token",
				Value: "ghp_should_be_ignored",
			},
		},
	}

	m := Merge(global, project, nil, "/tmp/test")
	if m.GitCredential != nil {
		t.Errorf("expected GitCredential to be nil when only set in project config, got %+v", m.GitCredential)
	}
}

func TestMerge_ToolCredentialFromContainer(t *testing.T) {
	global := &GlobalConfig{Shell: "zsh", User: "dev", DefaultImage: "fedora/43"}
	container := &ContainerConfig{
		Tools: map[string]ToolConfig{
			"gh": {
				Credential: &CredentialConfig{
					Source: "token",
					Env:    "GH_TOKEN",
				},
			},
		},
	}

	m := Merge(global, nil, container, "/tmp/test")
	if m.Tools == nil {
		t.Fatal("expected Tools from container config")
	}
	gh, ok := m.Tools["gh"]
	if !ok {
		t.Fatal("expected Tools[gh]")
	}
	if gh.Credential == nil || gh.Credential.Env != "GH_TOKEN" {
		t.Errorf("Tools[gh].Credential.Env = %v, want GH_TOKEN", gh.Credential)
	}
}

func TestMerge_ProjectToolCredentialIgnored(t *testing.T) {
	global := &GlobalConfig{Shell: "zsh", User: "dev", DefaultImage: "fedora/43"}
	project := &ProjectConfig{
		Tools: map[string]ToolConfig{
			"gh": {
				Credential: &CredentialConfig{
					Source: "token",
					Value: "ghp_should_be_ignored",
				},
			},
		},
	}

	m := Merge(global, project, nil, "/tmp/test")
	if m.Tools != nil {
		t.Errorf("expected Tools to be nil when only set in project config, got %+v", m.Tools)
	}
}
