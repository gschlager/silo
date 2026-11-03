package provision

import (
	"testing"

	"github.com/gschlager/silo/internal/config"
)

func TestAgentFunctions(t *testing.T) {
	agents := map[string]config.MergedAgentConfig{
		"claude":   {Enabled: true, Cmd: "claude --dangerously-skip-permissions"},
		"codex":    {Enabled: true, Cmd: "codex"},
		"plain":    {Enabled: true},
		"off":      {Enabled: false, Cmd: "off --flag"},
		"envvar":   {Enabled: true, Cmd: "FOO=1 envvar"},
		"bad.name": {Enabled: true, Cmd: "x --y"},
	}
	got := agentFunctions(agents)

	assertContains(t, got, `claude() { if [ -n "$SILO_AGENT_CMD" ]; then command claude "$@"; `+
		`else SILO_AGENT_CMD=1 command claude --dangerously-skip-permissions "$@"; fi; }`)
	assertNotContains(t, got, "codex()")
	assertNotContains(t, got, "plain()")
	assertNotContains(t, got, "off()")
	assertNotContains(t, got, "envvar()")
	assertNotContains(t, got, "bad.name()")
}
