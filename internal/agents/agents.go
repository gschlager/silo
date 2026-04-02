package agents

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
)

// GlobalDir returns the host-side shared agent directory.
// e.g. ~/.config/silo/agents/claude/
func GlobalDir(agent string) string {
	return filepath.Join(config.GlobalConfigDir(), "agents", agent)
}

// ContainerDir returns the host-side per-container agent directory.
// e.g. ~/.config/silo/containers/silo-myapp/agents/claude/
func ContainerDir(agent, containerName string) string {
	return filepath.Join(config.GlobalConfigDir(), "containers", containerName, "agents", agent)
}

// SyncToContainer copies shared files from the global agent dir into the
// container agent dir before launching an agent.
func SyncToContainer(agentName, containerName string, shared []string) {
	globalDir := GlobalDir(agentName)
	containerDir := ContainerDir(agentName, containerName)

	if err := os.MkdirAll(containerDir, 0700); err != nil {
		color.Warn("could not create container agent dir: %v", err)
		return
	}

	for _, name := range shared {
		src := filepath.Join(globalDir, name)
		dst := filepath.Join(containerDir, name)
		if err := copyPath(src, dst); err != nil {
			// Silently skip missing files (first run, nothing in global yet).
			if !os.IsNotExist(err) {
				color.Warn("could not sync %q to container: %v", name, err)
			}
		}
	}
}

// SyncFromContainer copies shared files from the container agent dir back
// to the global agent dir after an agent exits.
func SyncFromContainer(agentName, containerName string, shared []string) {
	globalDir := GlobalDir(agentName)
	containerDir := ContainerDir(agentName, containerName)

	if err := os.MkdirAll(globalDir, 0700); err != nil {
		color.Warn("could not create global agent dir: %v", err)
		return
	}

	for _, name := range shared {
		src := filepath.Join(containerDir, name)
		dst := filepath.Join(globalDir, name)
		if err := copyPath(src, dst); err != nil {
			if !os.IsNotExist(err) {
				color.Warn("could not sync %q from container: %v", name, err)
			}
		}
	}
}

// InstallAgents runs deps and install commands for each enabled agent.
func InstallAgents(ctx context.Context, server incuscli.InstanceServer, container, username, shell string, agents map[string]config.MergedAgentConfig, verbose bool) error {
	rootOpts := incus.ExecOpts{}
	userOpts := incus.UserOpts("/home/"+username, "/workspace")
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

// SetupAgentDirs creates per-container agent directories on the host and
// adds Incus disk devices to mount them into the container.
func SetupAgentDirs(ctx context.Context, server incuscli.InstanceServer, container, containerName string, agents map[string]config.MergedAgentConfig) error {
	for name, agent := range agents {
		if !agent.Enabled || agent.Home == "" {
			continue
		}

		dir := ContainerDir(name, containerName)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("creating agent dir for %q: %w", name, err)
		}

		deviceName := fmt.Sprintf("agent-%s", name)
		if err := incus.AddDiskDevice(ctx, server, container, deviceName, dir, agent.Home, false); err != nil {
			return fmt.Errorf("mounting agent dir for %q: %w", name, err)
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
