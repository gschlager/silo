package agents

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// ContainerHomeDir returns the host-side directory that is mounted into the
// container as the agent's home path. e.g. /home/dev/.claude/
// Host: ~/.config/silo/containers/silo-myapp/agents/claude/claude/home/
// The mode subdirectory isolates data between auth modes (claude, console, bedrock, etc.).
func ContainerHomeDir(agent, containerName, mode string) string {
	return filepath.Join(config.GlobalConfigDir(), "containers", containerName, "agents", agent, mode, "home")
}

// ContainerFilesDir returns the host-side directory for files that target paths
// outside the agent home. NOT mounted — synced via exec.
// Host: ~/.config/silo/containers/silo-myapp/agents/claude/claude/files/
func ContainerFilesDir(agent, containerName, mode string) string {
	return filepath.Join(config.GlobalConfigDir(), "containers", containerName, "agents", agent, mode, "files")
}

// SyncToContainer copies files from the global agent dir into the container's
// home/ or files/ subdirectory before launching an agent.
// - In-home files go to containers/.../agents/claude/home/ (mounted)
// - Out-of-home files go to containers/.../agents/claude/files/ (synced via exec)
func SyncToContainer(agentName, containerName, mode, agentHome, userHome string, rules []config.CopyRule) {
	globalDir := GlobalDir(agentName)
	homeDir := ContainerHomeDir(agentName, containerName, mode)
	filesDir := ContainerFilesDir(agentName, containerName, mode)

	os.MkdirAll(globalDir, 0700)
	os.MkdirAll(homeDir, 0700)
	os.MkdirAll(filesDir, 0700)

	for _, rule := range rules {
		src := filepath.Join(globalDir, rule.File)

		var dst string
		if rel := rule.RelPath(agentHome, userHome); rel != "" {
			dst = filepath.Join(homeDir, rel)
		} else {
			dst = filepath.Join(filesDir, rule.File)
		}

		if err := syncFile(src, dst, rule.Keys); err != nil {
			if !os.IsNotExist(err) {
				color.Warn("could not sync %q to container: %v", rule.File, err)
			}
		}
	}
}

// SyncFromContainer copies files from the container's home/ and files/
// subdirectories back to the global agent dir after an agent exits.
func SyncFromContainer(agentName, containerName, mode, agentHome, userHome string, rules []config.CopyRule) {
	globalDir := GlobalDir(agentName)
	homeDir := ContainerHomeDir(agentName, containerName, mode)
	filesDir := ContainerFilesDir(agentName, containerName, mode)

	os.MkdirAll(globalDir, 0700)

	for _, rule := range rules {
		var src string
		if rel := rule.RelPath(agentHome, userHome); rel != "" {
			src = filepath.Join(homeDir, rel)
		} else {
			src = filepath.Join(filesDir, rule.File)
		}

		dst := filepath.Join(globalDir, rule.File)

		if err := syncFile(src, dst, rule.Keys); err != nil {
			if !os.IsNotExist(err) {
				color.Warn("could not sync %q from container: %v", rule.File, err)
			}
		}
	}
}

// StartPeriodicSync runs SyncOutOfHomeFromContainer and SyncFromContainer in
// the background at the given interval so that token refreshes (including
// out-of-home files like claude.json) are propagated to the global agent dir
// while the agent is still running. Returns a stop function.
func StartPeriodicSync(ctx context.Context, server incuscli.InstanceServer, interval time.Duration, container, agentName, containerName, mode, agentHome, userHome string, rules []config.CopyRule) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				SyncOutOfHomeFromContainer(ctx, server, container, agentName, containerName, mode, agentHome, userHome, rules)
				SyncFromContainer(agentName, containerName, mode, agentHome, userHome, rules)
			}
		}
	}()
	return func() { close(done) }
}

// syncFile copies or merges a file. If keys is non-empty, only those
// JSON keys are merged; otherwise the whole file/dir is copied.
func syncFile(src, dst string, keys []string) error {
	if len(keys) > 0 {
		return mergeJSONKeys(src, dst, keys)
	}
	return copyPath(src, dst)
}

// ApplySet deep-merges the agent's set values into the appropriate files
// in the container's home/ and files/ dirs. Must be called after SyncToContainer.
func ApplySet(agentName, containerName, mode, agentHome, userHome string, rules []config.CopyRule, setValues map[string]map[string]any) {
	if len(setValues) == 0 {
		return
	}

	homeDir := ContainerHomeDir(agentName, containerName, mode)
	filesDir := ContainerFilesDir(agentName, containerName, mode)

	for target, values := range setValues {
		// Find the matching copy rule to determine if this is in-home or out-of-home.
		resolved := target
		if strings.HasPrefix(target, "~/") {
			resolved = filepath.Join("/home", "dev", target[2:]) // approximate
		}

		var filePath string
		for _, rule := range rules {
			if rule.Target == target {
				if rel := rule.RelPath(agentHome, userHome); rel != "" {
					filePath = filepath.Join(homeDir, rel)
				} else {
					filePath = filepath.Join(filesDir, rule.File)
				}
				break
			}
		}

		if filePath == "" {
			// No matching copy rule — write directly to files dir using sanitized target.
			sanitized := strings.ReplaceAll(strings.TrimPrefix(resolved, "/"), "/", "-")
			filePath = filepath.Join(filesDir, sanitized)
		}

		if err := deepMergeJSONFile(filePath, values); err != nil {
			color.Warn("could not apply set values to %q: %v", target, err)
		}
	}
}

// SyncOutOfHomeToContainer writes files whose target is outside the agent home
// directory into the running container via exec. Must be called after the
// container is running and SetupAgentDirs has mounted the agent home.
func SyncOutOfHomeToContainer(ctx context.Context, server incuscli.InstanceServer, container, agentName, containerName, mode, agentHome, userHome string, rules []config.CopyRule) {
	filesDir := ContainerFilesDir(agentName, containerName, mode)

	for _, rule := range rules {
		if rule.IsInsideHome(agentHome, userHome) {
			continue
		}

		target := rule.ResolveTarget(userHome)
		src := filepath.Join(filesDir, rule.File)

		// Read the full file from the container's files dir. This has
		// all the runtime data from the previous session.
		data, err := os.ReadFile(src)
		if err != nil {
			if !os.IsNotExist(err) {
				color.Warn("could not read %q for container: %v", rule.File, err)
			}
			continue
		}

		// Create parent dir and write file inside container.
		parentDir := filepath.Dir(target)
		incus.Exec(ctx, server, container, incus.ExecOpts{}, []string{
			"mkdir", "-p", parentDir,
		})
		if _, err := incus.ExecWithStdin(ctx, server, container, incus.ExecOpts{}, []string{
			"tee", target,
		}, data); err != nil {
			color.Warn("could not write %q in container: %v", target, err)
			continue
		}
		// Fix ownership.
		incus.Exec(ctx, server, container, incus.ExecOpts{}, []string{
			"chown", "1000:1000", target,
		})
	}
}

// SyncOutOfHomeFromContainer reads files whose target is outside the agent home
// directory from the running container and saves them to the container dir.
func SyncOutOfHomeFromContainer(ctx context.Context, server incuscli.InstanceServer, container, agentName, containerName, mode, agentHome, userHome string, rules []config.CopyRule) {
	filesDir := ContainerFilesDir(agentName, containerName, mode)

	for _, rule := range rules {
		if rule.IsInsideHome(agentHome, userHome) {
			continue
		}

		target := rule.ResolveTarget(userHome)
		dst := filepath.Join(filesDir, rule.File)

		// Read file from container.
		content, err := incus.Exec(ctx, server, container, incus.ExecOpts{}, []string{
			"cat", target,
		})
		if err != nil {
			// File might not exist yet (first run).
			continue
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			color.Warn("could not create dir for %q: %v", rule.File, err)
			continue
		}

		if len(rule.Keys) > 0 {
			// Write the full content first, then merge keys into the global file.
			if err := os.WriteFile(dst, []byte(content), 0600); err != nil {
				color.Warn("could not write %q: %v", rule.File, err)
			}
		} else {
			if err := os.WriteFile(dst, []byte(content), 0600); err != nil {
				color.Warn("could not write %q: %v", rule.File, err)
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

		homeDir := ContainerHomeDir(name, containerName, agent.Mode)
		if err := os.MkdirAll(homeDir, 0700); err != nil {
			return fmt.Errorf("creating agent home dir for %q: %w", name, err)
		}

		deviceName := fmt.Sprintf("agent-%s", name)
		if err := incus.AddDiskDevice(ctx, server, container, deviceName, homeDir, agent.Home, false); err != nil {
			return fmt.Errorf("mounting agent home dir for %q: %w", name, err)
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
