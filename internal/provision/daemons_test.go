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

	t.Run("runs in the project root", func(t *testing.T) {
		d := config.DaemonConfig{Cmd: "bin/rails server"}
		unit := buildUnitFile("rails", "bash", "/workspace/myapp", d)

		// segno-runner and anything else doing `git worktree add` relies on this.
		assertContains(t, unit, "WorkingDirectory=/workspace/myapp")
	})

	t.Run("per-daemon env is renamed from the prefixed manager variable", func(t *testing.T) {
		d := config.DaemonConfig{
			Cmd: "segno-runner codex",
			Env: map[string]string{"SEGNO_TOKEN": "op://Employee/segno-codex/credential"},
		}
		unit := buildUnitFile("segno-codex", "bash", "/workspace/myapp", d)

		assertContains(t, unit, `SEGNO_TOKEN="$$SILO_DAEMON_SEGNO_CODEX_SEGNO_TOKEN"`)
		assertContains(t, unit, "unset SILO_DAEMON_SEGNO_CODEX_SEGNO_TOKEN;")
		assertContains(t, unit, "segno-runner codex'")

		// The unit file lands on disk, so it must carry names only — never the
		// op:// reference and never a resolved value.
		assertNotContains(t, unit, "op://")
		assertNotContains(t, unit, "Environment=")
		assertNotContains(t, unit, "EnvironmentFile=")
	})

	t.Run("two daemons share a variable name without colliding", func(t *testing.T) {
		claude := buildUnitFile("segno-claude", "bash", "/workspace/app", config.DaemonConfig{
			Cmd: "segno-runner claude",
			Env: map[string]string{"SEGNO_TOKEN": "op://Employee/segno-claude/credential"},
		})
		codex := buildUnitFile("segno-codex", "bash", "/workspace/app", config.DaemonConfig{
			Cmd: "segno-runner codex",
			Env: map[string]string{"SEGNO_TOKEN": "op://Employee/segno-codex/credential"},
		})

		assertContains(t, claude, "$$SILO_DAEMON_SEGNO_CLAUDE_SEGNO_TOKEN")
		assertContains(t, codex, "$$SILO_DAEMON_SEGNO_CODEX_SEGNO_TOKEN")
	})

	t.Run("dollar signs in the command are left for the shell", func(t *testing.T) {
		d := config.DaemonConfig{Cmd: "bin/rails server -p $PORT"}
		unit := buildUnitFile("rails", "bash", "/workspace/myapp", d)

		// systemd expands $PORT into the unit's argv; $$ defers it to the shell.
		assertContains(t, unit, "bin/rails server -p $$PORT")
	})

	t.Run("no env means no preamble", func(t *testing.T) {
		d := config.DaemonConfig{Cmd: "bin/rails server"}
		unit := buildUnitFile("rails", "bash", "/workspace/myapp", d)

		assertNotContains(t, unit, "SILO_DAEMON_")
	})
}

func TestDaemonEnvVarName(t *testing.T) {
	cases := []struct{ daemon, key, want string }{
		{"rails", "PORT", "SILO_DAEMON_RAILS_PORT"},
		{"segno-codex", "SEGNO_TOKEN", "SILO_DAEMON_SEGNO_CODEX_SEGNO_TOKEN"},
		{"web.api", "URL", "SILO_DAEMON_WEB_API_URL"},
	}
	for _, c := range cases {
		if got := daemonEnvVarName(c.daemon, c.key); got != c.want {
			t.Errorf("daemonEnvVarName(%q, %q) = %q, want %q", c.daemon, c.key, got, c.want)
		}
	}
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
