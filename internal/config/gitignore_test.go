package config

import (
	"os"
	"strings"
	"testing"
)

func TestEnsureGitignore(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := EnsureGitignore(); err != nil {
		t.Fatal(err)
	}
	data, err := LoadGitignore()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".silo.yml", ".silo.local.yml", ".idea/", ".claude/"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("default gitignore missing %q:\n%s", want, data)
		}
	}

	// Idempotent: never overwrites an existing (possibly user-edited) file.
	if err := os.WriteFile(GitignorePath(), []byte("custom\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGitignore(); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadGitignore()
	if string(got) != "custom\n" {
		t.Errorf("EnsureGitignore overwrote existing file: %q", got)
	}
}
