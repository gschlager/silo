package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssignContainerNameKeepsLegacyNameAndHandlesCollision(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	first := filepath.Join(t.TempDir(), "app")
	second := filepath.Join(t.TempDir(), "app")
	for _, dir := range []string{first, second} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}

	firstName, err := AssignContainerName(first)
	if err != nil {
		t.Fatal(err)
	}
	if firstName != "silo-app" {
		t.Fatalf("first name = %q, want legacy name silo-app", firstName)
	}
	if again, err := AssignContainerName(first); err != nil || again != firstName {
		t.Fatalf("reloaded name = %q, %v; want %q", again, err, firstName)
	}

	secondName, err := AssignContainerName(second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secondName, "silo-app-") || len(secondName) != len("silo-app-")+16 {
		t.Fatalf("collision name = %q, want silo-app- plus 16 hex chars", secondName)
	}

	data, err := os.ReadFile(ProjectRegistryPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), first) || strings.Contains(string(data), second) {
		t.Fatalf("registry leaked canonical paths:\n%s", data)
	}
}

func TestAssignContainerNameCanonicalizesSymlinks(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	realDir := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}

	realName, err := AssignContainerName(realDir)
	if err != nil {
		t.Fatal(err)
	}
	linkName, err := AssignContainerName(link)
	if err != nil {
		t.Fatal(err)
	}
	if realName != linkName {
		t.Fatalf("canonical aliases got different assignments: %q and %q", realName, linkName)
	}
}
