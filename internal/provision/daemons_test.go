package provision

import (
	"strings"
	"testing"

	"github.com/gschlager/silo/internal/config"
)

func TestBuildUnitFile(t *testing.T) {
	t.Run("simple daemon", func(t *testing.T) {
		d := config.DaemonConfig{Cmd: "bin/rails server"}
		unit := buildUnitFile("rails", "/workspace/myapp", d)

		assertContains(t, unit, "Description=silo daemon: rails")
		assertContains(t, unit, "ExecStart=/bin/sh -c 'bin/rails server'")
		assertNotContains(t, unit, "After=")
		assertNotContains(t, unit, "Requires=")
	})

	t.Run("daemon with dependency", func(t *testing.T) {
		d := config.DaemonConfig{Cmd: "bin/ember-cli", After: "rails"}
		unit := buildUnitFile("ember", "/workspace/myapp", d)

		assertContains(t, unit, "Description=silo daemon: ember")
		assertContains(t, unit, "After=silo-rails.service")
		assertContains(t, unit, "Requires=silo-rails.service")
		assertContains(t, unit, "ExecStart=/bin/sh -c 'bin/ember-cli'")
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
