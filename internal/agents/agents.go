package agents

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
)

// DataDir returns the host-side agent data directory for a project.
func DataDir(agent, projectName string) string {
	return filepath.Join(config.GlobalConfigDir(), "agents", agent, projectName)
}

// InstallAgents runs deps and install commands for each enabled agent.
func InstallAgents(ctx context.Context, server incuscli.InstanceServer, container, username, shell string, agents map[string]config.MergedAgentConfig, verbose bool) error {
	rootOpts := incus.ExecOpts{}
	userOpts := incus.UserOpts("/home/"+username, "/workspace")
	for name, agent := range agents {
		if !agent.Enabled || agent.Install == "" {
			continue
		}
		// Install agent dependencies as root.
		if len(agent.Deps) > 0 {
			color.Status("Installing %s dependencies...", name)
			for _, dep := range agent.Deps {
				if err := execCmd(ctx, server, container, rootOpts, []string{"sh", "-c", dep}, verbose); err != nil {
					color.Warn("could not install deps for %s: %v", name, err)
				}
			}
		}
		// Install agent as user.
		color.Status("Installing %s...", name)
		if err := execCmd(ctx, server, container, userOpts, []string{"/bin/" + shell, "-lc", agent.Install}, verbose); err != nil {
			color.Warn("could not install %s: %v", name, err)
		}
	}
	return nil
}

func execCmd(ctx context.Context, server incuscli.InstanceServer, container string, opts incus.ExecOpts, command []string, verbose bool) error {
	if verbose {
		color.Command(command[len(command)-1])
		return incus.ExecStreaming(ctx, server, container, opts, command, os.Stdout, os.Stderr)
	}
	_, err := incus.Exec(ctx, server, container, opts, command)
	return err
}

// SetupAgentDirs creates agent data directories on the host, seeds files,
// and adds Incus disk devices to mount them into the container.
func SetupAgentDirs(ctx context.Context, server incuscli.InstanceServer, container, projectName string, agents map[string]config.MergedAgentConfig) error {
	for name, agent := range agents {
		if !agent.Enabled || agent.Home == "" {
			continue
		}

		dataDir := DataDir(name, projectName)
		if err := os.MkdirAll(dataDir, 0700); err != nil {
			return fmt.Errorf("creating agent data dir for %q: %w", name, err)
		}

		// Seed "once" files (only if they don't already exist in the data dir).
		for _, src := range agent.Seed.Once {
			if err := seedFile(src, dataDir, false); err != nil {
				color.Warn("could not seed %q for %s: %v", src, name, err)
			}
		}

		// Seed "always" files.
		for _, src := range agent.Seed.Always {
			if err := seedFile(src, dataDir, true); err != nil {
				color.Warn("could not seed %q for %s: %v", src, name, err)
			}
		}

		// Add Incus disk device.
		deviceName := fmt.Sprintf("agent-%s", name)
		if err := incus.AddDiskDevice(ctx, server, container, deviceName, dataDir, agent.Home, false); err != nil {
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
				color.Warn("could not refresh %q for %s: %v", src, name, err)
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
			return os.MkdirAll(dstPath, 0700)
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

	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
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
