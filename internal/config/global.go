package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	ProjectMounts map[string][]string `yaml:"project_mounts,omitempty"`
	Git           map[string]string   `yaml:"git,omitempty"`
	Agents        []AgentGlobalConfig `yaml:"agents"`

	// Presets are user-defined bundles a project opts into with `use:`, the
	// same key the built-in ruby/node presets use. They exist so something
	// needed by several projects — but not all of them — can be written once
	// without running everywhere: a global `daemons:` or `setup:` would start
	// in every container, which is rarely what a sidecar or a token-gated
	// installer wants.
	Presets map[string]UserPreset `yaml:"presets,omitempty"`
}

// UserPreset is a reusable bundle of setup commands, environment variables and
// daemons, defined once in the global config and activated per project via
// `use:`. Unlike the built-in presets (which are Go code and take parameters),
// a user preset is plain config.
type UserPreset struct {
	Setup []string `yaml:"setup"`
	// Env holds plain settings. Values are literals: they are written into the
	// container (/etc/environment.d and ~/.silo/env.sh) so every shell picks
	// them up, which also means they persist on disk. Put tokens in Secrets.
	Env map[string]string `yaml:"env"`
	// Secrets holds op:// references (or literals) resolved on the host and
	// passed as exec environment — never written to disk. They merge into the
	// same set as ~/.config/silo/secrets.yml, so they reach project setup,
	// enter/run/ra sessions and daemons, but only in projects that opted into
	// the preset with `use:`. The reserved "github" name behaves as it does in
	// secrets.yml: it wires the git credential helper and exports GH_TOKEN.
	Secrets map[string]string       `yaml:"secrets"`
	Daemons map[string]DaemonConfig `yaml:"daemons"`
}

// AgentGlobalConfig holds global agent settings.
type AgentGlobalConfig struct {
	Name    string                `yaml:"name"`
	Enabled bool                  `yaml:"enabled"`
	Cmd     string                `yaml:"cmd"`
	Deps    []string              `yaml:"deps"`
	Install string                `yaml:"install"`
	Mode    string                `yaml:"mode"`
	Links   []LinkRule            `yaml:"links"`
	Modes   map[string]ModeConfig `yaml:"modes,omitempty"`
}

// ModeConfig holds settings that apply when an agent runs in a given mode.
// Defined once in the global config, so switching an agent to the mode (via
// `silo mode`) carries this env automatically — no per-project repetition.
type ModeConfig struct {
	// Env is injected into the agent when this mode is active. Values are
	// literals or op:// references resolved from 1Password on the host at
	// launch time (never written to disk), so secrets like cloud credentials
	// can live here alongside plain settings.
	Env map[string]string `yaml:"env"`
}

// LinkRule defines a file or directory in the agent mode directory and where
// it should appear inside the container via symlink. The mode directory is
// mounted at /var/lib/silo/<agent>/ and symlinks point from the target path
// to the corresponding source within the mount.
type LinkRule struct {
	Source string `yaml:"source"` // path relative to mode dir (e.g. ".claude/", ".claude.json")
	Target string `yaml:"target"` // symlink path in container (supports ~/)
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
	if len(userCfg.ProjectMounts) > 0 {
		cfg.ProjectMounts = userCfg.ProjectMounts
	}
	if len(userCfg.Git) > 0 {
		cfg.Git = userCfg.Git
	}
	if len(userCfg.Presets) > 0 {
		cfg.Presets = userCfg.Presets
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
				if len(ua.Links) > 0 {
					da.Links = ua.Links
				}
				if len(ua.Modes) > 0 {
					da.Modes = ua.Modes
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
	for _, agent := range cfg.Agents {
		if err := ValidateAgentModePath(agent.Name, agent.Mode); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		for mode := range agent.Modes {
			if err := ValidateAgentModePath(agent.Name, mode); err != nil {
				return nil, fmt.Errorf("parsing %s: %w", path, err)
			}
		}
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
		DefaultImage: "fedora/44",
		DefaultSetup: []string{
			"dnf install -y git curl wget make gcc which jq socat ripgrep fd-find tree gh ncurses",
		},
		PassEnv: []string{"TERM", "COLORTERM", "COLORFGBG", "LANG", "LC_ALL"},
		Shell:   "bash",
		User:    "dev",
		Git:     map[string]string{},
		Agents: []AgentGlobalConfig{
			{
				Name:    "claude",
				Enabled: true,
				Cmd:     "claude",
				Install: "curl -fsSL https://claude.ai/install.sh | bash",
				Mode:    "oauth",
				Links: []LinkRule{
					{Source: ".claude/", Target: "~/.claude/"},
					{Source: ".claude.json", Target: "~/.claude.json"},
				},
			},
			{
				Name:    "codex",
				Enabled: true,
				Cmd:     "codex",
				Deps:    []string{"dnf install -y nodejs npm bubblewrap"},
				Install: "npm install -g @openai/codex --prefix ~/.local",
				Mode:    "console",
				Links: []LinkRule{
					{Source: ".codex/", Target: "~/.codex/"},
				},
			},
		},
	}
}

// ValidateEnv rejects env: entries that hold a 1Password reference. Unlike
// secrets:, agents[].modes[].env and a daemon's env:, an env: value is never
// resolved — it is written into the container verbatim, and it lands on disk in
// /etc/environment.d and ~/.silo/env.sh. Left unchecked, an op:// value there
// silently becomes a literal, so whatever consumes it fails somewhere far from
// the cause (a curl that 401s during setup, say). Fail at load time instead, and
// say where the value belongs.
func ValidateEnv(global *GlobalConfig, project *ProjectConfig, use UseList) error {
	for _, u := range use {
		p, ok := global.Presets[u.Name]
		if !ok {
			continue
		}
		if name, ok := firstSecretRef(p.Env); ok {
			return fmt.Errorf("preset %q: env value for %s is a 1Password reference, "+
				"but env: values are written into the container as literals and are never resolved; "+
				"move it to the preset's secrets: block", u.Name, name)
		}
	}

	if project != nil {
		if name, ok := firstSecretRef(project.Env); ok {
			return fmt.Errorf("env value for %s is a 1Password reference, "+
				"but env: values are written into the container as literals and are never resolved; "+
				"move it to %s", name, SecretsPath())
		}
	}

	return nil
}

// firstSecretRef returns the alphabetically first key whose value looks like a
// 1Password reference. Sorted so the same entry is reported every run rather
// than whichever one map iteration reached first.
func firstSecretRef(env map[string]string) (string, bool) {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if strings.HasPrefix(env[name], "op://") {
			return name, true
		}
	}
	return "", false
}
