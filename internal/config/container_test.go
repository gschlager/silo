package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContainerConfigPath(t *testing.T) {
	// Override XDG_CONFIG_HOME so the path is predictable.
	t.Setenv("XDG_CONFIG_HOME", "/tmp/config")

	got := ContainerConfigPath("silo-myapp")
	want := "/tmp/config/silo/containers/silo-myapp/config.yml"
	if got != want {
		t.Errorf("ContainerConfigPath = %q, want %q", got, want)
	}
}

func TestLoadContainerConfig_NotExists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, err := LoadContainerConfig("silo-nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.GitCredential != nil {
		t.Errorf("GitCredential = %v, want nil", cfg.GitCredential)
	}
	if cfg.Tools != nil {
		t.Errorf("Tools = %v, want nil", cfg.Tools)
	}
}

func TestLoadContainerConfig_WithCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	containerDir := filepath.Join(dir, "silo", "containers", "silo-myapp")
	if err := os.MkdirAll(containerDir, 0700); err != nil {
		t.Fatal(err)
	}

	content := `git_credential:
  source: 1password
  ref: op://Vault/Item/token
tools:
  gh:
    credential:
      source: token
      env: GH_TOKEN
`
	if err := os.WriteFile(filepath.Join(containerDir, "config.yml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadContainerConfig("silo-myapp")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitCredential == nil {
		t.Fatal("expected GitCredential, got nil")
	}
	if cfg.GitCredential.Source != "1password" {
		t.Errorf("GitCredential.Source = %q, want %q", cfg.GitCredential.Source, "1password")
	}
	if cfg.GitCredential.Ref != "op://Vault/Item/token" {
		t.Errorf("GitCredential.Ref = %q, want %q", cfg.GitCredential.Ref, "op://Vault/Item/token")
	}
	if cfg.Tools == nil {
		t.Fatal("expected Tools, got nil")
	}
	gh, ok := cfg.Tools["gh"]
	if !ok {
		t.Fatal("expected Tools[gh]")
	}
	if gh.Credential == nil {
		t.Fatal("expected Tools[gh].Credential, got nil")
	}
	if gh.Credential.Source != "token" {
		t.Errorf("Tools[gh].Credential.Source = %q, want %q", gh.Credential.Source, "token")
	}
	if gh.Credential.Env != "GH_TOKEN" {
		t.Errorf("Tools[gh].Credential.Env = %q, want %q", gh.Credential.Env, "GH_TOKEN")
	}
}

func TestSaveAndLoadContainerConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	original := &ContainerConfig{
		GitCredential: &CredentialConfig{
			Source: "token",
			Value:  "ghp_test123",
		},
		Tools: map[string]ToolConfig{
			"gh": {
				Credential: &CredentialConfig{
					Source: "1password",
					Ref:    "op://Vault/PAT/token",
				},
			},
		},
	}

	if err := SaveContainerConfig("silo-roundtrip", original); err != nil {
		t.Fatal(err)
	}

	// Verify file permissions.
	path := ContainerConfigPath("silo-roundtrip")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}

	loaded, err := LoadContainerConfig("silo-roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GitCredential == nil {
		t.Fatal("expected GitCredential after round-trip")
	}
	if loaded.GitCredential.Source != "token" {
		t.Errorf("GitCredential.Source = %q, want %q", loaded.GitCredential.Source, "token")
	}
	if loaded.GitCredential.Value != "ghp_test123" {
		t.Errorf("GitCredential.Value = %q, want %q", loaded.GitCredential.Value, "ghp_test123")
	}
	if loaded.Tools == nil {
		t.Fatal("expected Tools after round-trip")
	}
	gh := loaded.Tools["gh"]
	if gh.Credential == nil {
		t.Fatal("expected Tools[gh].Credential after round-trip")
	}
	if gh.Credential.Ref != "op://Vault/PAT/token" {
		t.Errorf("Tools[gh].Credential.Ref = %q, want %q", gh.Credential.Ref, "op://Vault/PAT/token")
	}
}
