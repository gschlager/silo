package agents

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
)

// DataDir returns the host-side agent data directory for a project.
func DataDir(agent, projectName string) string {
	return filepath.Join(config.GlobalConfigDir(), "agents", agent, projectName)
}

// SetupAgentDirs creates agent data directories on the host, seeds files,
// and adds Incus disk devices to mount them into the container.
func SetupAgentDirs(server incuscli.InstanceServer, container, projectName string, agents map[string]config.MergedAgentConfig) error {
	for name, agent := range agents {
		if agent.Home == "" {
			continue
		}

		dataDir := DataDir(name, projectName)
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return fmt.Errorf("creating agent data dir for %q: %w", name, err)
		}

		// Seed "once" files (only if they don't already exist in the data dir).
		for _, src := range agent.Seed.Once {
			if err := seedFile(src, dataDir, false); err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: could not seed %q for %s: %v\n", src, name, err)
			}
		}

		// Seed "always" files.
		for _, src := range agent.Seed.Always {
			if err := seedFile(src, dataDir, true); err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: could not seed %q for %s: %v\n", src, name, err)
			}
		}

		// Add Incus disk device.
		deviceName := fmt.Sprintf("agent-%s", name)
		if err := incus.AddDiskDevice(server, container, deviceName, dataDir, agent.Home, false); err != nil {
			return fmt.Errorf("mounting agent data dir for %q: %w", name, err)
		}
	}
	return nil
}

// RefreshAlwaysSeeds re-copies "always" seed files to pick up token refreshes.
func RefreshAlwaysSeeds(projectName string, agents map[string]config.MergedAgentConfig) error {
	for name, agent := range agents {
		dataDir := DataDir(name, projectName)
		for _, src := range agent.Seed.Always {
			if err := seedFile(src, dataDir, true); err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: could not refresh %q for %s: %v\n", src, name, err)
			}
		}
	}
	return nil
}

// CleanupAgentDirs removes the host-side agent data directories for a project.
func CleanupAgentDirs(projectName string, agentNames []string) error {
	for _, name := range agentNames {
		dataDir := DataDir(name, projectName)
		if err := os.RemoveAll(dataDir); err != nil {
			return fmt.Errorf("removing agent data dir for %q: %w", name, err)
		}
	}
	return nil
}

// seedFile copies a file from the host into the agent data directory.
// src is a path like "~/.claude/.credentials.json".
// If overwrite is false, skips files that already exist in the destination.
func seedFile(src, dataDir string, overwrite bool) error {
	expanded := expandHome(src)

	srcInfo, err := os.Stat(expanded)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // silently skip missing source files
		}
		return err
	}

	// Get the filename (last component of the source path).
	name := filepath.Base(expanded)
	dst := filepath.Join(dataDir, name)

	if srcInfo.IsDir() {
		return seedDir(expanded, dst, overwrite)
	}

	if !overwrite {
		if _, err := os.Stat(dst); err == nil {
			return nil // already exists, skip
		}
	}

	return copyFile(expanded, dst)
}

func seedDir(src, dst string, overwrite bool) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(src, path)
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, 0755)
		}

		if !overwrite {
			if _, err := os.Stat(dstPath); err == nil {
				return nil
			}
		}

		return copyFile(path, dstPath)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	// Preserve permissions.
	srcInfo, _ := os.Stat(src)
	return os.Chmod(dst, srcInfo.Mode())
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
