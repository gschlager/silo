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
	Name    string     `yaml:"name"`
	Enabled bool       `yaml:"enabled"`
	Cmd     string     `yaml:"cmd"`
	Deps    []string   `yaml:"deps"`
	Install string     `yaml:"install"`
	Mode    string     `yaml:"mode"`
	Home    string                       `yaml:"home"`
	Copy    []CopyRule                   `yaml:"copy"`
	Set     map[string]map[string]any    `yaml:"set,omitempty"`
}

// CopyRule defines how a file is synced between silo's agent directory and the container.
type CopyRule struct {
	File   string   `yaml:"file"`           // name in silo's agent dir
	Target string   `yaml:"target"`         // path inside container (supports ~/)
	Keys   []string `yaml:"keys,omitempty"` // for JSON files: only sync these top-level keys
}

// agentOverride is used during config parsing to detect explicit enabled: false.
type agentOverride struct {
	Name    string `yaml:"name"`
	Enabled *bool  `yaml:"enabled"`
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

	// Parse user config separately. Use agentOverride with *bool for enabled
	// so we can distinguish "not set" from "explicitly false".
	var userCfg GlobalConfig
	if err := yaml.Unmarshal(data, &userCfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var userAgentOverrides struct {
		Agents []agentOverride `yaml:"agents"`
	}
	yaml.Unmarshal(data, &userAgentOverrides)

	// Start with defaults, then apply user overrides for scalar fields.
	cfg := defaultGlobalConfig()
	if userCfg.DefaultImage != "" {
		cfg.DefaultImage = userCfg.DefaultImage
	}
	if len(userCfg.DefaultSetup) > 0 {
		cfg.DefaultSetup = userCfg.DefaultSetup
	}
	if userCfg.DefaultAgent != "" {
		cfg.DefaultAgent = userCfg.DefaultAgent
	}
	if len(userCfg.PassEnv) > 0 {
		cfg.PassEnv = userCfg.PassEnv
	}
	if userCfg.Shell != "" {
		cfg.Shell = userCfg.Shell
	}
	if userCfg.User != "" {
		cfg.User = userCfg.User
	}
	if userCfg.Notifications {
		cfg.Notifications = true
	}
	if len(userCfg.Mounts) > 0 {
		cfg.Mounts = userCfg.Mounts
	}
	if len(userCfg.Git) > 0 {
		cfg.Git = userCfg.Git
	}

	// Merge agents: user overrides per agent by name, defaults fill in the rest.
	if len(userCfg.Agents) > 0 {
		// Build enabled override map from the *bool parse.
		enabledOverrides := make(map[string]*bool)
		for _, ao := range userAgentOverrides.Agents {
			if ao.Enabled != nil {
				enabledOverrides[ao.Name] = ao.Enabled
			}
		}

		defaultAgents := make(map[string]AgentGlobalConfig)
		for _, a := range cfg.Agents {
			defaultAgents[a.Name] = a
		}

		// Apply user overrides onto defaults.
		for _, ua := range userCfg.Agents {
			if da, ok := defaultAgents[ua.Name]; ok {
				if ua.Cmd != "" {
					da.Cmd = ua.Cmd
				}
				if len(ua.Deps) > 0 {
					da.Deps = ua.Deps
				}
				if ua.Install != "" {
					da.Install = ua.Install
				}
				if ua.Mode != "" {
					da.Mode = ua.Mode
				}
				if ua.Home != "" {
					da.Home = ua.Home
				}
				if len(ua.Copy) > 0 {
					da.Copy = ua.Copy
				}
				if len(ua.Set) > 0 {
					da.Set = ua.Set
				}
				if ep, ok := enabledOverrides[ua.Name]; ok {
					da.Enabled = *ep
				}
				defaultAgents[ua.Name] = da
			} else {
				// New agent defined by user — default to enabled unless explicit.
				if ua.Enabled == false {
					if ep, ok := enabledOverrides[ua.Name]; ok {
						ua.Enabled = *ep
					} else {
						ua.Enabled = true
					}
				}
				defaultAgents[ua.Name] = ua
			}
		}

		// Rebuild agents list preserving default order, then appending new user agents.
		var merged []AgentGlobalConfig
		seen := make(map[string]bool)
		for _, a := range cfg.Agents {
			if m, ok := defaultAgents[a.Name]; ok {
				merged = append(merged, m)
				seen[a.Name] = true
			}
		}
		for _, ua := range userCfg.Agents {
			if !seen[ua.Name] {
				merged = append(merged, defaultAgents[ua.Name])
			}
		}
		cfg.Agents = merged
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
			"dnf install -y git curl wget make gcc which zsh jq socat ripgrep fd-find tree gh ncurses",
		},
		PassEnv: []string{"TERM", "COLORTERM", "COLORFGBG", "LANG", "LC_ALL"},
		Shell:   "zsh",
		User:  "dev",
		Git:   map[string]string{},
		Agents: []AgentGlobalConfig{
			{
				Name:    "claude",
				Enabled: true,
				Cmd:     "claude",
				Install: "curl -fsSL https://claude.ai/install.sh | bash",
				Mode:    "oauth",
				Home: "/home/dev/.claude",
				Copy: []CopyRule{
					{File: ".credentials.json", Target: "~/.claude/.credentials.json"},
					{File: "claude.json", Target: "~/.claude.json", Keys: []string{"oauthAccount", "userID", "hasCompletedOnboarding"}},
					{File: "settings.json", Target: "~/.claude/settings.json"},
					{File: "hooks/", Target: "~/.claude/hooks/"},
				},
				Set: map[string]map[string]any{
					"~/.claude.json": {
						"projects": map[string]any{
							"/workspace": map[string]any{
								"hasTrustDialogAccepted": true,
							},
						},
					},
				},
			},
			{
				Name:    "codex",
				Enabled: true,
				Cmd:     "codex",
				Deps:    []string{"dnf install -y nodejs npm bubblewrap"},
				Install: "npm install -g @openai/codex --prefix ~/.local",
				Mode:    "api-key",
				Home: "/home/dev/.codex",
				Copy: []CopyRule{
					{File: "config.toml", Target: "~/.codex/config.toml"},
				},
			},
		},
	}
}
