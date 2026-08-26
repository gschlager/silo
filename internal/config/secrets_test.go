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

func TestAddProjectSecret(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	added, err := AddProjectSecret("proj", "github", "op://v/i/f")
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("expected the secret to be added")
	}
	m, _ := SecretsForProject("proj")
	if m["github"] != "op://v/i/f" {
		t.Errorf("github = %q, want op://v/i/f", m["github"])
	}

	// An existing entry is never overwritten.
	added2, err := AddProjectSecret("proj", "github", "op://other")
	if err != nil {
		t.Fatal(err)
	}
	if added2 {
		t.Error("expected no overwrite for an existing project")
	}
	m2, _ := SecretsForProject("proj")
	if m2["github"] != "op://v/i/f" {
		t.Errorf("github should be unchanged, got %q", m2["github"])
	}
}

func TestProjectName(t *testing.T) {
	m := &MergedConfig{ContainerName: ContainerName("/home/dev/migrations_tooling")}
	if got := m.ProjectName(); !strings.HasPrefix(got, "migrations-tooling-") || len(got) != len("migrations-tooling-")+16 {
		t.Errorf("ProjectName = %q, want migrations-tooling- plus 16 hex characters", got)
	}
}

func TestMigrateSecretsProjectKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeSecrets(t, `# keep this comment
.shared: &shared
  AWS_REGION: eu-central-1

migrations-tooling:
  <<: *shared
  github: op://Employee/github/token # keep inline comment
`)

	current := "migrations-tooling-15d3424052ab84c7"
	result, err := MigrateSecretsProjectKey("migrations-tooling", current)
	if err != nil {
		t.Fatal(err)
	}
	if result != SecretsMigrated {
		t.Fatalf("result = %v, want SecretsMigrated", result)
	}

	secrets, err := LoadSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := secrets["migrations-tooling"]; exists {
		t.Fatal("legacy key still exists")
	}
	if got := secrets[current]["github"]; got != "op://Employee/github/token" {
		t.Fatalf("migrated github secret = %q", got)
	}
	if got := secrets[current]["AWS_REGION"]; got != "eu-central-1" {
		t.Fatalf("merged anchor value = %q", got)
	}
	data, err := os.ReadFile(SecretsPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# keep this comment") || !strings.Contains(string(data), "# keep inline comment") {
		t.Fatalf("comments were not preserved:\n%s", data)
	}
	info, err := os.Stat(SecretsPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("secrets permissions = %o, want 600", got)
	}

	result, err = MigrateSecretsProjectKey("migrations-tooling", current)
	if err != nil {
		t.Fatal(err)
	}
	if result != SecretsAlreadyCurrent {
		t.Fatalf("second result = %v, want SecretsAlreadyCurrent", result)
	}
}

func TestMigrateSecretsProjectKeyConflictDoesNotWrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	original := "legacy:\n  github: old\ncurrent-hash:\n  github: new\n"
	writeSecrets(t, original)

	if _, err := MigrateSecretsProjectKey("legacy", "current-hash"); err == nil {
		t.Fatal("expected conflict error")
	}
	data, err := os.ReadFile(SecretsPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("conflicting migration changed file:\n%s", data)
	}
}

func TestMigrateSecretsProjectKeyMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	result, err := MigrateSecretsProjectKey("legacy", "current-hash")
	if err != nil {
		t.Fatal(err)
	}
	if result != SecretsLegacyMissing {
		t.Fatalf("result = %v, want SecretsLegacyMissing", result)
	}
}
