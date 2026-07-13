package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A user config that adds a modes block to the built-in claude agent must parse
// and survive the default-merge, so `silo mode claude bedrock` picks it up.
func TestLoadGlobalConfig_ParsesModes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "silo")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	yaml := `agents:
  - name: claude
    modes:
      bedrock:
        env:
          CLAUDE_CODE_USE_BEDROCK: "1"
          AWS_ACCESS_KEY_ID: op://Employee/aws-bedrock/access-key-id
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yml"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	var claude *AgentGlobalConfig
	for i := range cfg.Agents {
		if cfg.Agents[i].Name == "claude" {
			claude = &cfg.Agents[i]
		}
	}
	if claude == nil {
		t.Fatal("claude agent missing after merge")
	}
	env := claude.Modes["bedrock"].Env
	if env["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("CLAUDE_CODE_USE_BEDROCK = %q, want 1", env["CLAUDE_CODE_USE_BEDROCK"])
	}
	if env["AWS_ACCESS_KEY_ID"] != "op://Employee/aws-bedrock/access-key-id" {
		t.Errorf("op:// ref not preserved: %q", env["AWS_ACCESS_KEY_ID"])
	}
	// The default install command must still be present (default-merge preserved).
	if claude.Install == "" {
		t.Error("adding modes wiped the agent's default install command")
	}
}
