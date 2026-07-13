package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gschlager/silo/internal/config"
)

// CleanupContainerDirs must wipe the ephemeral per-container state but keep the
// mode selection, so `silo rm` followed by `silo up` restores the same auth mode
// instead of silently reverting to the config default.
func TestCleanupContainerDirsPreservesModeState(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	const container = "silo-test-project"
	if err := config.SaveModeState(container, map[string]string{"claude": "bedrock"}); err != nil {
		t.Fatalf("SaveModeState: %v", err)
	}
	if err := config.SaveContainerShell(container, "zsh"); err != nil {
		t.Fatalf("SaveContainerShell: %v", err)
	}
	// An unrelated ephemeral file that should not survive.
	base := filepath.Join(config.GlobalConfigDir(), "containers", container)
	if err := os.WriteFile(filepath.Join(base, "scratch"), []byte("x"), 0600); err != nil {
		t.Fatalf("writing scratch file: %v", err)
	}

	if err := CleanupContainerDirs(container); err != nil {
		t.Fatalf("CleanupContainerDirs: %v", err)
	}

	modes, err := config.LoadModeState(container)
	if err != nil {
		t.Fatalf("LoadModeState: %v", err)
	}
	if modes["claude"] != "bedrock" {
		t.Errorf("mode selection not preserved: got %q, want %q", modes["claude"], "bedrock")
	}

	if got := config.LoadContainerShell(container); got != "" {
		t.Errorf("shell marker should have been removed, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(base, "scratch")); !os.IsNotExist(err) {
		t.Errorf("scratch file should have been removed, stat err = %v", err)
	}
}

// A missing container dir is not an error (nothing to clean up).
func TestCleanupContainerDirsMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := CleanupContainerDirs("never-created"); err != nil {
		t.Errorf("expected nil for missing dir, got %v", err)
	}
}
