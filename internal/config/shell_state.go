package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ContainerShellPath returns the path to the per-container effective-shell marker.
func ContainerShellPath(containerName string) string {
	return filepath.Join(GlobalConfigDir(), "containers", containerName, "shell")
}

// SaveContainerShell records the shell silo actually provisioned the container
// with. The configured shell may differ from the effective one when it could not
// be installed and silo fell back to bash; later commands read this back so
// enter/run/daemons all use the same shell.
func SaveContainerShell(containerName, shell string) error {
	path := ContainerShellPath(containerName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(shell+"\n"), 0600)
}

// LoadContainerShell returns the recorded effective shell for a container, or ""
// if none was recorded (e.g. the container predates this marker).
func LoadContainerShell(containerName string) string {
	data, err := os.ReadFile(ContainerShellPath(containerName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
