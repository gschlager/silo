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

// ModeDir returns the host-side shared directory for an agent mode.
// e.g. ~/.config/silo/agents/claude/oauth/
func ModeDir(agent, mode string) string {
	return filepath.Join(config.GlobalConfigDir(), "agents", agent, mode)
}

// EnsureModeDir creates the mode directory if it doesn't exist and seeds it
// with files from the host. Idempotent — does nothing if the dir already has content.
func EnsureModeDir(agent, mode string, links []config.LinkRule) error {
	modeDir := ModeDir(agent, mode)

	// Check if already populated (has any content beyond README).
	if entries, err := os.ReadDir(modeDir); err == nil && hasNonREADME(entries) {
		return nil
	}

	if err := os.MkdirAll(modeDir, 0700); err != nil {
		return fmt.Errorf("creating mode dir for %s/%s: %w", agent, mode, err)
	}

	// Write a README so the directory doesn't look empty (hidden files).
	writeREADME(modeDir, agent, mode)

	// Seed from host: for each link rule, copy from the host path if source doesn't exist.
	hostHome, _ := os.UserHomeDir()
	if hostHome != "" {
		for _, link := range links {
			src := link.ResolveTarget(hostHome)
			dst := filepath.Join(modeDir, link.Source)

			if err := copyPath(src, dst); err != nil {
				if !os.IsNotExist(err) {
					color.Warn("could not seed %q from host: %v", link.Source, err)
				}
			}
		}
	}

	return nil
}

// SetupAgentDirs mounts agent mode directories into the container and creates
// symlinks from where agents expect their config files.
func SetupAgentDirs(ctx context.Context, server incuscli.InstanceServer, container, containerName string, agents map[string]config.MergedAgentConfig) error {
	userHome := "/home/dev"

	for name, agent := range agents {
		if !agent.Enabled || len(agent.Links) == 0 {
			continue
		}

		modeDir := ModeDir(name, agent.Mode)
		if err := os.MkdirAll(modeDir, 0700); err != nil {
			return fmt.Errorf("creating mode dir for %q: %w", name, err)
		}

		// Mount the entire mode dir at /run/silo/<agent>/.
		mountPath := fmt.Sprintf("/run/silo/%s", name)
		deviceName := fmt.Sprintf("agent-%s", name)
		if err := incus.AddDiskDevice(ctx, server, container, deviceName, modeDir, mountPath, false); err != nil {
			return fmt.Errorf("mounting mode dir for %q: %w", name, err)
		}

		// Create symlinks inside the container for each link rule.
		for _, link := range agent.Links {
			target := link.ResolveTarget(userHome)
			symlinkSrc := fmt.Sprintf("%s/%s", mountPath, link.Source)

			// Remove trailing slash for symlink creation.
			target = strings.TrimRight(target, "/")
			symlinkSrc = strings.TrimRight(symlinkSrc, "/")

			// Ensure parent dir exists, remove any existing file/dir, create symlink.
			parentDir := filepath.Dir(target)
			incus.Exec(ctx, server, container, incus.ExecOpts{}, []string{
				"sh", "-c", fmt.Sprintf("mkdir -p %s && rm -rf %s && ln -sf %s %s",
					parentDir, target, symlinkSrc, target),
			})
			// Fix symlink ownership.
			incus.Exec(ctx, server, container, incus.ExecOpts{}, []string{
				"chown", "-h", "1000:1000", target,
			})
		}
	}
	return nil
}

// InstallAgents runs deps and install commands for each enabled agent.
func InstallAgents(ctx context.Context, server incuscli.InstanceServer, container, username, shell string, agents map[string]config.MergedAgentConfig, verbose bool) error {
	rootOpts := incus.ExecOpts{}
	userOpts := incus.UserOpts("/home/"+username, "/home/"+username)
	for name, agent := range agents {
		if !agent.Enabled || agent.Install == "" {
			continue
		}
		if len(agent.Deps) > 0 {
			color.Status("Installing %s dependencies...", name)
			for _, dep := range agent.Deps {
				if err := execCmd(ctx, server, container, rootOpts, []string{"sh", "-c", dep}, verbose); err != nil {
					color.Warn("could not install deps for %s: %v", name, err)
				}
			}
		}
		color.Status("Installing %s...", name)
		if err := execCmd(ctx, server, container, userOpts, []string{"/bin/" + shell, "-lc", agent.Install}, verbose); err != nil {
			color.Warn("could not install %s: %v", name, err)
		}
	}
	return nil
}

// CleanupContainerDirs removes the host-side per-container data directory.
func CleanupContainerDirs(containerName string) error {
	containerBase := filepath.Join(config.GlobalConfigDir(), "containers", containerName)
	return os.RemoveAll(containerBase)
}

func execCmd(ctx context.Context, server incuscli.InstanceServer, container string, opts incus.ExecOpts, command []string, verbose bool) error {
	if verbose {
		color.Command(command[len(command)-1])
		return incus.ExecStreaming(ctx, server, container, opts, command, os.Stdout, os.Stderr)
	}
	_, err := incus.Exec(ctx, server, container, opts, command)
	return err
}

func hasNonREADME(entries []os.DirEntry) bool {
	for _, e := range entries {
		if e.Name() != "README" {
			return true
		}
	}
	return false
}

func writeREADME(modeDir, agent, mode string) {
	content := fmt.Sprintf(`This directory contains %s configuration for the %q authentication mode.
It is shared across all silo containers using this mode.

Files here are mounted into containers at /run/silo/%s/ and symlinked
into the home directory. Changes made by the agent inside any container
are immediately visible to all containers sharing this mode.
`, agent, mode, agent)
	os.WriteFile(filepath.Join(modeDir, "README"), []byte(content), 0644)
}

// copyPath copies a file or directory from src to dst, creating parent dirs.
func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(src, path)
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, 0700)
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

	srcInfo, _ := os.Stat(src)
	return os.Chmod(dst, srcInfo.Mode())
}

