package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v3"
)

// ProjectRegistryPath is the host-owned mapping from path-scoped project keys
// to stable container names. Most projects retain the traditional readable
// name; a hash is added to the container name only when that name is occupied.
func ProjectRegistryPath() string {
	return filepath.Join(GlobalConfigDir(), "projects.yml")
}

type projectRegistry struct {
	Projects map[string]string `yaml:"projects"`
}

// AssignContainerName returns the stable container name allocated to a project.
// Allocation is serialized so two same-basename projects cannot both claim the
// unsuffixed name. The registry contains only path hashes, not host paths.
func AssignContainerName(projectDir string) (name string, err error) {
	dir := GlobalConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}

	lockPath := filepath.Join(dir, ".projects.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", fmt.Errorf("opening project registry lock: %w", err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return "", fmt.Errorf("locking project registry: %w", err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN) //nolint:errcheck

	registry := projectRegistry{Projects: make(map[string]string)}
	path := ProjectRegistryPath()
	data, readErr := os.ReadFile(path)
	if readErr == nil {
		if err := yaml.Unmarshal(data, &registry); err != nil {
			return "", fmt.Errorf("parsing %s: %w", path, err)
		}
		if registry.Projects == nil {
			registry.Projects = make(map[string]string)
		}
	} else if !os.IsNotExist(readErr) {
		return "", fmt.Errorf("reading %s: %w", path, readErr)
	}

	key := ProjectName(projectDir)
	if assigned, ok := registry.Projects[key]; ok {
		if !validContainerName(assigned) {
			return "", fmt.Errorf("invalid container name %q for project %q in %s", assigned, key, path)
		}
		return assigned, nil
	}

	assigned := ContainerName(projectDir)
	for _, used := range registry.Projects {
		if used == assigned {
			assigned = collisionContainerName(projectDir)
			break
		}
	}
	registry.Projects[key] = assigned
	encoded, err := MarshalYAML(&registry)
	if err != nil {
		return "", fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := writeFileAtomic(path, encoded, 0600); err != nil {
		return "", err
	}
	return assigned, nil
}

func validContainerName(name string) bool {
	if len(name) < len("silo-a") || len(name) > 63 || !strings.HasPrefix(name, "silo-") {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}
