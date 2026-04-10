package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ContainerConfig holds per-container secrets stored outside the repository.
// Location: ~/.config/silo/containers/<container-name>/config.yml
type ContainerConfig struct {
	GitCredential *CredentialConfig     `yaml:"git_credential"`
	Tools         map[string]ToolConfig `yaml:"tools"`
}

// ContainerConfigPath returns the path to the per-container config file.
func ContainerConfigPath(containerName string) string {
	return filepath.Join(GlobalConfigDir(), "containers", containerName, "config.yml")
}

// LoadContainerConfig reads the per-container secrets config.
// Returns an empty ContainerConfig if the file doesn't exist.
func LoadContainerConfig(containerName string) (*ContainerConfig, error) {
	path := ContainerConfigPath(containerName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ContainerConfig{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var cfg ContainerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

// SaveContainerConfig writes the per-container secrets config.
func SaveContainerConfig(containerName string, cfg *ContainerConfig) error {
	path := ContainerConfigPath(containerName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating directory for %s: %w", path, err)
	}

	data, err := MarshalYAML(cfg)
	if err != nil {
		return fmt.Errorf("marshaling container config: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}
