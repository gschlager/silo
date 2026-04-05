package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ProjectConfig represents the .silo.yml project configuration.
type ProjectConfig struct {
	Image   string              `yaml:"image"`
	Setup   []string            `yaml:"setup"`
	Sync    []string            `yaml:"sync"`
	Reset   map[string][]string `yaml:"reset"`
	Update  []string            `yaml:"update"`
	Ports   []string            `yaml:"ports"`
	Env     map[string]string   `yaml:"env"`
	Git     GitConfig           `yaml:"git"`
	Agents  map[string]AgentProjectConfig `yaml:"agents"`
	Mounts  []string            `yaml:"mounts"`
	Tools   map[string]ToolConfig `yaml:"tools"`
	Daemons map[string]DaemonConfig `yaml:"daemons"`
	Nesting bool                `yaml:"nesting"`
}

// GitConfig holds git settings and optional credential configuration.
type GitConfig struct {
	Settings   map[string]string `yaml:",inline"`
	Credential *CredentialConfig `yaml:"credential"`
}

// CredentialConfig describes how to resolve a credential.
type CredentialConfig struct {
	Source string `yaml:"source"` // "1password", "token"
	Ref    string `yaml:"ref"`    // op:// reference for 1password
	Value  string `yaml:"value"`  // literal token value
	Env    string `yaml:"env"`    // environment variable name
}

// AgentProjectConfig holds per-project agent settings.
type AgentProjectConfig struct {
	Enabled *bool             `yaml:"enabled"` // nil = use default (true)
	Mode    string            `yaml:"mode"`    // "oauth", "bedrock", "api-key"
	Env     map[string]string `yaml:"env"`
}

// ToolConfig holds configuration for a tool like gh.
type ToolConfig struct {
	Credential *CredentialConfig `yaml:"credential"`
}

// DaemonConfig holds daemon configuration, supporting both string and object forms.
type DaemonConfig struct {
	Cmd       string   `yaml:"cmd"`
	Autostart bool     `yaml:"autostart"`
	After     string   `yaml:"after"`
	Ports     []string `yaml:"ports"`
}

// UnmarshalYAML implements custom unmarshaling for DaemonConfig.
// Supports both "rails: bin/rails server" (string) and object form with cmd/autostart/after/ports.
func (d *DaemonConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		d.Cmd = value.Value
		d.Autostart = true
		return nil
	}

	// Object form — need a temporary type to avoid infinite recursion.
	type rawDaemon struct {
		Cmd       string   `yaml:"cmd"`
		Autostart *bool    `yaml:"autostart"`
		After     string   `yaml:"after"`
		Ports     []string `yaml:"ports"`
	}
	var raw rawDaemon
	if err := value.Decode(&raw); err != nil {
		return err
	}
	d.Cmd = raw.Cmd
	if raw.Autostart == nil {
		d.Autostart = true
	} else {
		d.Autostart = *raw.Autostart
	}
	d.After = raw.After
	d.Ports = raw.Ports
	return nil
}

// LoadProjectConfig reads .silo.yml (or .silo.yaml) from the given directory.
// If .silo.local.yml (or .silo.local.yaml) exists, its values are merged on
// top, allowing per-machine overrides without modifying the shared config.
// Returns nil with no error if no config file is found.
func LoadProjectConfig(dir string) (*ProjectConfig, error) {
	cfg := loadProjectFile(dir, ".silo.yml", ".silo.yaml")
	local := loadProjectFile(dir, ".silo.local.yml", ".silo.local.yaml")

	if cfg == nil && local == nil {
		return nil, nil
	}

	if cfg != nil && cfg.err != nil {
		return nil, cfg.err
	}
	if local != nil && local.err != nil {
		return nil, local.err
	}

	if cfg == nil {
		return &local.config, nil
	}
	if local == nil {
		return &cfg.config, nil
	}

	merged := mergeProjectConfigs(&cfg.config, &local.config)
	return merged, nil
}

type projectFileResult struct {
	config ProjectConfig
	err    error
}

func loadProjectFile(dir string, names ...string) *projectFileResult {
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return &projectFileResult{err: fmt.Errorf("reading %s: %w", path, err)}
		}

		var cfg ProjectConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return &projectFileResult{err: fmt.Errorf("parsing %s: %w", path, err)}
		}
		return &projectFileResult{config: cfg}
	}
	return nil
}

// mergeProjectConfigs overlays local on top of base. Non-zero local values
// replace base values; maps and slices from local replace (not append) base.
func mergeProjectConfigs(base, local *ProjectConfig) *ProjectConfig {
	m := *base

	if local.Image != "" {
		m.Image = local.Image
	}
	if local.Setup != nil {
		m.Setup = local.Setup
	}
	if local.Sync != nil {
		m.Sync = local.Sync
	}
	if local.Reset != nil {
		m.Reset = local.Reset
	}
	if local.Update != nil {
		m.Update = local.Update
	}
	if local.Ports != nil {
		m.Ports = local.Ports
	}
	if local.Env != nil {
		m.Env = local.Env
	}
	if local.Git.Settings != nil || local.Git.Credential != nil {
		m.Git = local.Git
	}
	if local.Agents != nil {
		m.Agents = local.Agents
	}
	if local.Mounts != nil {
		m.Mounts = local.Mounts
	}
	if local.Tools != nil {
		m.Tools = local.Tools
	}
	if local.Daemons != nil {
		m.Daemons = local.Daemons
	}
	if local.Nesting {
		m.Nesting = local.Nesting
	}

	return &m
}
