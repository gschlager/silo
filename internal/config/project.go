package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProjectConfig represents the .silo.yml project configuration.
type ProjectConfig struct {
	Image   string              `yaml:"image"`
	Setup   []string            `yaml:"setup"`
	Sync    []string            `yaml:"sync"`
	Reset   map[string][]string `yaml:"reset"`
	Update  []string            `yaml:"update"`
	Ports   []PortForward       `yaml:"ports"`
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
	Mode    string            `yaml:"mode"`    // "claude", "console", "bedrock", "vertex", "foundry"
	Env     map[string]string `yaml:"env"`
}

// ToolConfig holds configuration for a tool like gh.
type ToolConfig struct {
	Credential *CredentialConfig `yaml:"credential"`
}

// DaemonConfig holds daemon configuration, supporting both string and object forms.
type DaemonConfig struct {
	Cmd       string        `yaml:"cmd"`
	Autostart bool          `yaml:"autostart"`
	After     string        `yaml:"after"`
	Ports     []PortForward `yaml:"ports"`
}

// PortForward is a single port forward. In YAML it accepts either the shorthand
// string form ("5432:15432" or "3000") or a mapping form so the forward can be
// named for clearer status output and Incus device names:
//
//	ports:
//	  - 5432:15432
//	  - name: web
//	    port: 9292:9292
type PortForward struct {
	Name string // optional label; drives status display and the device name
	Spec string // "container:host" or "container"
}

// UnmarshalYAML accepts a bare port spec string or a {name, port} mapping.
func (p *PortForward) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		// Use the raw value so both "5432:15432" (string) and 3000 (int) work.
		p.Spec = value.Value
		return nil
	case yaml.MappingNode:
		var raw struct {
			Name string    `yaml:"name"`
			Port yaml.Node `yaml:"port"`
		}
		if err := value.Decode(&raw); err != nil {
			return err
		}
		if raw.Port.Kind != yaml.ScalarNode || raw.Port.Value == "" {
			return fmt.Errorf("port forward %q: 'port' must be a spec like 5432:15432", raw.Name)
		}
		p.Name = raw.Name
		p.Spec = raw.Port.Value
		return nil
	default:
		return fmt.Errorf("invalid port forward entry")
	}
}

// MarshalYAML writes the shorthand string form when unnamed and the mapping form
// otherwise, so generated configs stay compact.
func (p PortForward) MarshalYAML() (any, error) {
	if p.Name == "" {
		return p.Spec, nil
	}
	return struct {
		Name string `yaml:"name"`
		Port string `yaml:"port"`
	}{p.Name, p.Spec}, nil
}

// DeviceName returns the Incus proxy-device name for this forward. Named forwards
// use their sanitized name; unnamed ones use the host port (unique per forward).
// Both are stable across config edits, unlike a slice index.
func (p PortForward) DeviceName(hostPort int) string {
	if s := sanitizeDeviceSuffix(p.Name); s != "" {
		return "port-" + s
	}
	return "port-" + strconv.Itoa(hostPort)
}

// sanitizeDeviceSuffix lowercases s and replaces any character that is not a
// letter, digit, or hyphen with a hyphen. Returns "" for an empty/blank name.
func sanitizeDeviceSuffix(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
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
		Cmd       string        `yaml:"cmd"`
		Autostart *bool         `yaml:"autostart"`
		After     string        `yaml:"after"`
		Ports     []PortForward `yaml:"ports"`
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
