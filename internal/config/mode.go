package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ModeStatePath returns the path to the per-container mode state file.
func ModeStatePath(containerName string) string {
	return filepath.Join(GlobalConfigDir(), "containers", containerName, "mode.yml")
}

// LoadModeState reads the per-container mode overrides.
// Returns an empty map if the file doesn't exist.
func LoadModeState(containerName string) (map[string]string, error) {
	path := ModeStatePath(containerName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var modes map[string]string
	if err := yaml.Unmarshal(data, &modes); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if modes == nil {
		modes = map[string]string{}
	}
	return modes, nil
}

// SaveModeState writes the per-container mode overrides.
func SaveModeState(containerName string, modes map[string]string) error {
	path := ModeStatePath(containerName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating directory for %s: %w", path, err)
	}

	data, err := MarshalYAML(modes)
	if err != nil {
		return fmt.Errorf("marshaling mode state: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}
