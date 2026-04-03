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
	if len(cfg.Ports) != 1 || cfg.Ports[0] != "8080:80" {
		t.Errorf("Ports = %v, want [8080:80]", cfg.Ports)
	}
	// Env should be replaced by local.
	if cfg.Env["FOO"] != "override" {
		t.Errorf("Env[FOO] = %q, want %q", cfg.Env["FOO"], "override")
	}
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

func writeYAML(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
