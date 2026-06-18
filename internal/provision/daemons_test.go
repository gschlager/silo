package provision

import (
	"strings"
	"testing"

	"github.com/gschlager/silo/internal/config"
)

func TestBuildUnitFile(t *testing.T) {
	t.Run("simple daemon", func(t *testing.T) {
		d := config.DaemonConfig{Cmd: "bin/rails server"}
		unit := buildUnitFile("rails", "zsh", "/workspace/myapp", d)

		assertContains(t, unit, "Description=silo daemon: rails")
		assertContains(t, unit, "ExecStart=/bin/zsh -lc 'bin/rails server'")
		assertNotContains(t, unit, "After=")
		assertNotContains(t, unit, "Requires=")
	})

	t.Run("daemon with dependency", func(t *testing.T) {
		d := config.DaemonConfig{Cmd: "bin/ember-cli", After: "rails"}
		unit := buildUnitFile("ember", "bash", "/workspace/myapp", d)

		assertContains(t, unit, "Description=silo daemon: ember")
		assertContains(t, unit, "After=silo-rails.service")
		assertContains(t, unit, "Requires=silo-rails.service")
		assertContains(t, unit, "ExecStart=/bin/bash -lc 'bin/ember-cli'")
	})
}

func TestEnvInjectionScript(t *testing.T) {
	t.Run("exports values and imports by name", func(t *testing.T) {
		script := envInjectionScript(map[string]string{
			"RAILS_ENV":    "development",
			"GITHUB_TOKEN": "ghp_secret",
		})

		assertContains(t, script, "export RAILS_ENV='development'")
		assertContains(t, script, "export GITHUB_TOKEN='ghp_secret'")
		// Names are imported (sorted); values never appear as arguments.
		assertContains(t, script, "systemctl --user import-environment GITHUB_TOKEN RAILS_ENV")
		assertNotContains(t, script, "import-environment ghp_secret")
	})

	t.Run("single-quote escapes values", func(t *testing.T) {
		script := envInjectionScript(map[string]string{"MSG": "it's set"})
		assertContains(t, script, `export MSG='it'\''s set'`)
	})

	t.Run("PATH expands instead of being single-quoted", func(t *testing.T) {
		script := envInjectionScript(map[string]string{"PATH": "/opt/bin:$PATH"})
		// A single-quoted PATH would freeze $PATH literally and drop the system
		// dirs, breaking the systemctl call that follows.
		assertNotContains(t, script, `export PATH='/opt/bin:$PATH'`)
		assertContains(t, script, `export PATH="/opt/bin:$PATH"`)
	})
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", s, substr)
	}
}

func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("expected %q not to contain %q", s, substr)
	}
}
