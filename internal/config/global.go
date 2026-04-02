package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// MarshalYAML marshals a value to YAML with 2-space indentation.
func MarshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GlobalConfig represents ~/.config/silo/config.yml.
type GlobalConfig struct {
	DefaultImage  string              `yaml:"default_image"`
	DefaultSetup  []string            `yaml:"default_setup"`
	DefaultAgent  string              `yaml:"default_agent,omitempty"`
	PassEnv       []string            `yaml:"pass_env,omitempty"`
	Shell         string              `yaml:"shell"`
	User          string              `yaml:"user"`
	Notifications bool                `yaml:"notifications,omitempty"`
	Mounts        []string            `yaml:"mounts,omitempty"`
	Git           map[string]string   `yaml:"git,omitempty"`
	Agents        []AgentGlobalConfig `yaml:"agents"`
}

// AgentGlobalConfig holds global agent settings.
type AgentGlobalConfig struct {
	Name    string          `yaml:"name"`
	Cmd     string          `yaml:"cmd,omitempty"`
	Deps    []string        `yaml:"deps,omitempty"`
	Install string          `yaml:"install,omitempty"`
	Mode    string          `yaml:"mode,omitempty"`
	Home    string          `yaml:"home,omitempty"`
	Seed    AgentSeedConfig `yaml:"seed,omitempty"`
}

// AgentSeedConfig lists files to seed into agent data directories.
type AgentSeedConfig struct {
	Always []string `yaml:"always,omitempty"`
	Once   []string `yaml:"once,omitempty"`
}

// GlobalConfigDir returns the silo config directory path.
func GlobalConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "silo")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "silo")
}

// GlobalConfigPath returns the full path to the global config file.
func GlobalConfigPath() string {
	return filepath.Join(GlobalConfigDir(), "config.yml")
}

// LoadGlobalConfig reads the global config file.
// Returns a config with defaults if the file doesn't exist.
func LoadGlobalConfig() (*GlobalConfig, error) {
	path := GlobalConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultGlobalConfig(), nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	cfg := defaultGlobalConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return cfg, nil
}

// EnsureGlobalConfig creates the global config directory and a minimal
// config file if it doesn't exist. Defaults are applied in code, not
// written to disk — the file only needs to contain user overrides.
func EnsureGlobalConfig() error {
	path := GlobalConfigPath()
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	content := `# Silo global configuration
# Run 'silo config show' to see all resolved settings with defaults.
# Only add settings here that you want to override.
`

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func defaultGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		DefaultImage: "fedora/43",
		DefaultSetup: []string{
			"dnf install -y git curl wget make gcc which zsh jq socat ripgrep fd-find tree gh",
		},
		PassEnv: []string{"TERM", "COLORTERM", "COLORFGBG", "LANG", "LC_ALL"},
		Shell:   "zsh",
		User:  "dev",
		Git:   map[string]string{},
		Agents: []AgentGlobalConfig{
			{
				Name:    "claude",
				Install: "curl -fsSL https://claude.ai/install.sh | bash",
				Mode:    "oauth",
				Home:    "/home/dev/.claude",
				Seed: AgentSeedConfig{
					Always: []string{
						"~/.claude/.credentials.json",
						"~/.claude/settings.json",
					},
					Once: []string{
						"~/.claude/hooks",
					},
				},
			},
			{
				Name:    "codex",
				Deps:    []string{"dnf install -y nodejs npm bubblewrap"},
				Install: "npm install -g @openai/codex --prefix ~/.local",
				Mode:    "api-key",
				Home:    "/home/dev/.codex",
				Seed: AgentSeedConfig{
					Always: []string{
						"~/.codex/auth.json",
					},
				},
			},
		},
	}
}
