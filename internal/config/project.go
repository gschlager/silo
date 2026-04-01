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
	Docker  bool                `yaml:"docker"`
	Compose string              `yaml:"compose"`
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
	Mode string            `yaml:"mode"` // "oauth", "bedrock", "api-key"
	Env  map[string]string `yaml:"env"`
}

// ToolConfig holds configuration for a tool like gh.
type ToolConfig struct {
	Credential *CredentialConfig `yaml:"credential"`
}

// DaemonConfig holds daemon configuration, supporting both string and object forms.
type DaemonConfig struct {
	Cmd       string `yaml:"cmd"`
	Autostart bool   `yaml:"autostart"`
}

// UnmarshalYAML implements custom unmarshaling for DaemonConfig.
// Supports both "rails: bin/rails server" (string) and object form with cmd/autostart.
func (d *DaemonConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		d.Cmd = value.Value
		d.Autostart = true
		return nil
	}

	// Object form — need a temporary type to avoid infinite recursion.
	type rawDaemon struct {
		Cmd       string `yaml:"cmd"`
		Autostart *bool  `yaml:"autostart"`
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
	return nil
}

// LoadProjectConfig reads .silo.yml (or .silo.yaml) from the given directory.
// Returns nil with no error if no config file is found.
func LoadProjectConfig(dir string) (*ProjectConfig, error) {
	for _, name := range []string{".silo.yml", ".silo.yaml"} {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}

		var cfg ProjectConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		return &cfg, nil
	}
	return nil, nil
}
