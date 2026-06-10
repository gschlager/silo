package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSecrets(t *testing.T, content string) {
	t.Helper()
	dir := filepath.Join(GlobalConfigDir())
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SecretsPath(), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSecretsForProject(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeSecrets(t, `
converters:
  github: op://Emp/conv/token
  AWS_BEARER_TOKEN_BEDROCK: op://Emp/bedrock/key
`)

	m, err := SecretsForProject("converters")
	if err != nil {
		t.Fatal(err)
	}
	if m["github"] != "op://Emp/conv/token" {
		t.Errorf("github = %q", m["github"])
	}
	if m["AWS_BEARER_TOKEN_BEDROCK"] != "op://Emp/bedrock/key" {
		t.Errorf("bedrock = %q", m["AWS_BEARER_TOKEN_BEDROCK"])
	}

	none, err := SecretsForProject("missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("unknown project should have no secrets, got %v", none)
	}
}

func TestSecretsMissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m, err := SecretsForProject("x")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty, got %v", m)
	}
}

func TestEnsureSecretsStub(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	added, err := EnsureSecretsStub("myproj")
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("expected a stub to be added")
	}

	// Idempotent: the key now parses (as null), so no second stub is added.
	added2, err := EnsureSecretsStub("myproj")
	if err != nil {
		t.Fatal(err)
	}
	if added2 {
		t.Error("expected no second stub for the same project")
	}

	// The stub resolves to no secrets.
	m, err := SecretsForProject("myproj")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("stub should resolve to no secrets, got %v", m)
	}

	// A second project is appended without clobbering the first.
	if _, err := EnsureSecretsStub("other"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(SecretsPath())
	if !strings.Contains(string(data), "myproj:") || !strings.Contains(string(data), "other:") {
		t.Errorf("both stubs should be present:\n%s", data)
	}
}

func TestProjectName(t *testing.T) {
	m := &MergedConfig{ContainerName: ContainerName("/home/dev/migrations_tooling")}
	if got := m.ProjectName(); got != "migrations-tooling" {
		t.Errorf("ProjectName = %q, want migrations-tooling", got)
	}
}
