package cache

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DataDir returns the silo data directory (for caches and other non-config data).
func DataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "silo")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "silo")
}

// Dir returns the host-side cache directory for a given container path.
// If the path is in the shared list, returns a shared directory.
// Otherwise returns a per-container isolated directory.
func Dir(containerName, path string, shared []string) string {
	sanitized := sanitizePath(path)
	if slices.Contains(shared, path) {
		return filepath.Join(DataDir(), "cache", "shared", sanitized)
	}
	return filepath.Join(DataDir(), "cache", containerName, sanitized)
}

// sanitizePath converts a container path to a safe directory name.
// e.g. "/home/dev/.rubies" → "home-dev-.rubies"
func sanitizePath(path string) string {
	path = strings.TrimPrefix(path, "/")
	return strings.ReplaceAll(path, "/", "-")
}
