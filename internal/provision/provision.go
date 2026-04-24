package provision

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/agents"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
)

// ProvisionMinimal creates a lightweight container with just networking, a user,
// and a single agent installed. Used for temporary containers like silo init --auto.
func ProvisionMinimal(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig, agentName string) error {
	name := cfg.ContainerName

	status("Creating temporary container %s...", name)
	if err := incus.Launch(ctx, server, cfg.Image, name); err != nil {
		return err
	}

	status("Waiting for network...")
	networkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := incus.WaitForNetwork(networkCtx, server, name, "dns.google"); err != nil {
		return err
	}

	// Mount project directory.
	status("Mounting project directory...")
	if err := incus.AddDiskDevice(ctx, server, name, "workspace", cfg.ProjectDir, cfg.WorkspacePath(), false); err != nil {
		return err
	}

	// Run default_setup.
	if len(cfg.DefaultSetup) > 0 {
		status("Running default setup...")
		if err := runCommands(ctx, server, name, incus.ExecOpts{}, cfg.DefaultSetup); err != nil {
			return fmt.Errorf("default_setup failed: %w", err)
		}
	}

	// Create user.
	status("Creating user %s...", cfg.User)
	if err := CreateUser(ctx, server, name, cfg.User, cfg.Shell); err != nil {
		return err
	}

	// Install the requested agent.
	agentCfg, ok := cfg.Agents[agentName]
	if !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	if agentCfg.Install != "" {
		singleAgent := map[string]config.MergedAgentConfig{agentName: agentCfg}
		if err := agents.InstallAgents(ctx, server, name, cfg.User, cfg.Shell, singleAgent, false); err != nil {
			return err
		}
	}

	// Set up agent data directory.
	if len(agentCfg.Links) > 0 {
		status("Setting up agent directory...")
		singleAgent := map[string]config.MergedAgentConfig{agentName: agentCfg}
		if err := agents.SetupAgentDirs(ctx, server, name, cfg.ContainerName, singleAgent); err != nil {
			return err
		}
	}

	return nil
}

// Provision runs the full first-run provisioning flow for a container.
func Provision(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig, verbose bool) error {
	verboseOutput = verbose
	name := cfg.ContainerName

	// Step 1: Create and start the container.
	status("Creating container %s from %s...", name, cfg.Image)
	if err := incus.Launch(ctx, server, cfg.Image, name); err != nil {
		return err
	}

	// Clean up the container if provisioning fails.
	success := false
	defer func() {
		if !success {
			color.Warn("Provisioning failed. Removing container %s...", name)
			incus.Stop(context.Background(), server, name)
			incus.Delete(context.Background(), server, name)
		}
	}()

	// Silo lazily starts containers on demand; keep Incus from auto-starting
	// every silo container on host boot.
	if err := incus.SetConfig(ctx, server, name, "boot.autostart", "false"); err != nil {
		return fmt.Errorf("disabling autostart: %w", err)
	}

	// Step 2: Wait for network.
	status("Waiting for network...")
	networkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := incus.WaitForNetwork(networkCtx, server, name, "dns.google"); err != nil {
		return err
	}

	// Step 3: Container nesting (required for Docker, Podman, etc.).
	if cfg.Nesting {
		status("Enabling container nesting...")
		if err := incus.SetConfig(ctx, server, name, "security.nesting", "true"); err != nil {
			return err
		}
	}

	// Step 3: Set up port forwards.
	for i, portSpec := range cfg.Ports {
		containerPort, hostPort, err := parsePortSpec(portSpec)
		if err != nil {
			return fmt.Errorf("invalid port spec %q: %w", portSpec, err)
		}
		status("Adding port forward %d -> %d...", containerPort, hostPort)
		deviceName := fmt.Sprintf("port-%d", i)
		if err := incus.AddProxyDevice(ctx, server, name, deviceName, hostPort, containerPort); err != nil {
			return err
		}
	}

	// Step 4: Mount the project directory.
	status("Mounting project directory...")
	if err := incus.AddDiskDevice(ctx, server, name, "workspace", cfg.ProjectDir, cfg.WorkspacePath(), false); err != nil {
		return err
	}

	// Step 5: Set up additional mounts.
	for i, mountSpec := range cfg.Mounts {
		source, target, readonly, err := parseMountSpec(mountSpec)
		if err != nil {
			return fmt.Errorf("invalid mount spec %q: %w", mountSpec, err)
		}
		// Expand ~ in source path.
		if strings.HasPrefix(source, "~/") {
			home, _ := os.UserHomeDir()
			source = home + source[1:]
		}
		// Ensure source exists.
		if err := os.MkdirAll(source, 0755); err != nil {
			return fmt.Errorf("creating mount source %q: %w", source, err)
		}
		status("Mounting %s -> %s...", source, target)
		deviceName := fmt.Sprintf("mount-%d", i)
		if err := incus.AddDiskDevice(ctx, server, name, deviceName, source, target, readonly); err != nil {
			return err
		}
	}

	// Step 6: Run default_setup (as root, before user creation so shell is available).
	if len(cfg.DefaultSetup) > 0 {
		status("Running default setup...")
		if err := runCommands(ctx, server, name, incus.ExecOpts{}, cfg.DefaultSetup); err != nil {
			return fmt.Errorf("default_setup failed: %w", err)
		}
	}

	// Step 7: Create dev user.
	status("Creating user %s...", cfg.User)
	if err := CreateUser(ctx, server, name, cfg.User, cfg.Shell); err != nil {
		return err
	}

	// Step 8: Configure git.
	if len(cfg.Git) > 0 {
		status("Configuring git...")
		if err := ConfigureGit(ctx, server, name, cfg.User, cfg.Git); err != nil {
			return err
		}
	}

	// Step 9: Set up git credential helper.
	if cfg.GitCredential != nil {
		status("Setting up git credentials...")
		if err := SetupCredentialHelper(ctx, server, name, cfg.User, cfg.GitCredential); err != nil {
			return err
		}
	}

	// Step 10: Resolve tool credentials (passed at runtime, not baked in).

	// Step 11: Install agents.
	if len(cfg.Agents) > 0 {
		if err := agents.InstallAgents(ctx, server, name, cfg.User, cfg.Shell, cfg.Agents, verbose); err != nil {
			return err
		}
	}

	// Step 12: Set up agent data directories.
	if len(cfg.Agents) > 0 {
		status("Setting up agent directories...")
		if err := agents.SetupAgentDirs(ctx, server, name, cfg.ContainerName, cfg.Agents); err != nil {
			return err
		}
	}

	// Step 12: Set timezone and locale.
	status("Configuring timezone and locale...")
	if err := configureTimezoneAndLocale(ctx, server, name); err != nil {
		color.Warn("could not set timezone/locale: %v", err)
	}

	// Step 13: Set environment variables.
	if len(cfg.Env) > 0 {
		status("Setting environment variables...")
		if err := setEnvironment(ctx, server, name, cfg.User, cfg.Shell, cfg.Env); err != nil {
			return err
		}
	}

	// Step 14: Run project setup (as dev user with login shell).
	if len(cfg.Setup) > 0 {
		status("Running project setup...")
		userOpts := incus.UserOpts("/home/"+cfg.User, cfg.WorkspacePath())
		if err := runUserCommands(ctx, server, name, userOpts, cfg.Shell, cfg.Setup); err != nil {
			return fmt.Errorf("setup failed: %w", err)
		}
	}

	// Step 15: Set up daemons.
	if len(cfg.Daemons) > 0 {
		status("Setting up daemons...")
		if err := SetupDaemons(ctx, server, name, cfg.User, cfg.WorkspacePath(), cfg.Daemons); err != nil {
			return err
		}
	}

	// Step 16: Set up notifications (persistent pieces only — the listener
	// is started per-session by silo ra / silo enter).
	if cfg.Notifications {
		status("Setting up notifications...")
		if err := SetupNotifications(ctx, server, name); err != nil {
			color.Warn("could not set up notifications: %v", err)
		}
	}

	// Step 17: Mark as initialized.
	rootOpts := incus.ExecOpts{}
	if _, err := incus.Exec(ctx, server, name, rootOpts, []string{
		"sh", "-c", fmt.Sprintf("touch /home/%s/.silo-initialized", cfg.User),
	}); err != nil {
		return fmt.Errorf("creating init marker: %w", err)
	}

	// Step 18: Take initial snapshot.
	status("Taking initial snapshot...")
	if err := incus.CreateSnapshot(ctx, server, name, "initial"); err != nil {
		color.Warn("could not create initial snapshot: %v", err)
	}

	success = true
	status("Environment ready!")
	return nil
}

// IsInitialized checks if a container has been fully provisioned.
// Reads the marker file via the Incus file API so it works on stopped
// containers — exec would require a running container and falsely report
// "not initialized" for any stopped container.
func IsInitialized(server incuscli.InstanceServer, container, username string) bool {
	path := fmt.Sprintf("/home/%s/.silo-initialized", username)
	content, _, err := server.GetInstanceFile(container, path)
	if err != nil {
		return false
	}
	if content != nil {
		content.Close()
	}
	return true
}

var verboseOutput bool

// runUserCommands runs commands as the dev user with a login shell so that
// ~/.profile is sourced and PATH includes user-installed binaries.
func runUserCommands(ctx context.Context, server incuscli.InstanceServer, container string, opts incus.ExecOpts, shell string, commands []string) error {
	loginShell := "/bin/" + shell
	for _, cmd := range commands {
		if verboseOutput {
			color.Command(cmd)
			if err := incus.ExecStreaming(ctx, server, container, opts, []string{loginShell, "-lc", cmd}, os.Stdout, os.Stderr); err != nil {
				return fmt.Errorf("command %q: %w", cmd, err)
			}
		} else {
			if _, err := incus.Exec(ctx, server, container, opts, []string{loginShell, "-lc", cmd}); err != nil {
				return fmt.Errorf("command %q: %w", cmd, err)
			}
		}
	}
	return nil
}

func runCommands(ctx context.Context, server incuscli.InstanceServer, container string, opts incus.ExecOpts, commands []string) error {
	for _, cmd := range commands {
		if verboseOutput {
			color.Command(cmd)
			if err := incus.ExecStreaming(ctx, server, container, opts, []string{"sh", "-c", cmd}, os.Stdout, os.Stderr); err != nil {
				return fmt.Errorf("command %q: %w", cmd, err)
			}
		} else {
			if _, err := incus.Exec(ctx, server, container, opts, []string{"sh", "-c", cmd}); err != nil {
				return fmt.Errorf("command %q: %w", cmd, err)
			}
		}
	}
	return nil
}

// ResolveToolEnv resolves tool credentials into environment variables.
// Runs on the host — suitable for passing as env to exec sessions.
// Returns an error on the first credential that fails to resolve, so callers
// don't launch sessions (especially agents) with silently missing tokens.
func ResolveToolEnv(tools map[string]config.ToolConfig) (map[string]string, error) {
	env := make(map[string]string)
	for name, tool := range tools {
		if tool.Credential == nil {
			continue
		}

		token, err := resolveCredential(tool.Credential)
		if err != nil {
			return nil, fmt.Errorf("resolving credential for tool %q: %w", name, err)
		}

		switch name {
		case "gh":
			env["GH_TOKEN"] = token
		default:
			env[strings.ToUpper(name)+"_TOKEN"] = token
		}
	}
	return env, nil
}

func configureTimezoneAndLocale(ctx context.Context, server incuscli.InstanceServer, container string) error {
	rootOpts := incus.ExecOpts{}

	// Copy host timezone.
	tz, err := os.ReadFile("/etc/timezone")
	if err == nil {
		tzName := strings.TrimSpace(string(tz))
		incus.Exec(ctx, server, container, rootOpts, []string{
			"ln", "-sf", "/usr/share/zoneinfo/" + tzName, "/etc/localtime",
		})
	} else {
		// Try reading from timedatectl or /etc/localtime symlink.
		if target, err := os.Readlink("/etc/localtime"); err == nil {
			incus.Exec(ctx, server, container, rootOpts, []string{
				"ln", "-sf", target, "/etc/localtime",
			})
		}
	}

	// Copy host locale.
	lang := os.Getenv("LANG")
	if lang != "" {
		incus.Exec(ctx, server, container, rootOpts, []string{
			"sh", "-c", fmt.Sprintf(`echo 'LANG=%s' > /etc/locale.conf`, lang),
		})
	}

	return nil
}

// shellEscape returns a single-quoted string safe for embedding in shell
// commands. Any embedded single quotes are replaced with the sequence '\''.
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func setEnvironment(ctx context.Context, server incuscli.InstanceServer, container, username, shell string, env map[string]string) error {
	rootOpts := incus.ExecOpts{}

	// Ensure directory exists (no user-controlled data in this command).
	if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
		"mkdir", "-p", "/etc/environment.d",
	}); err != nil {
		return fmt.Errorf("creating environment.d: %w", err)
	}

	// Write environment variables to /etc/environment.d/ for system-wide
	// availability. Pipe the content via stdin to avoid any shell interpretation.
	var lines []string
	for k, v := range env {
		lines = append(lines, fmt.Sprintf("%s=%s", k, v))
	}
	content := strings.Join(lines, "\n") + "\n"

	if _, err := incus.ExecWithStdin(ctx, server, container, rootOpts, []string{
		"tee", "-a", "/etc/environment.d/silo.conf",
	}, []byte(content)); err != nil {
		return fmt.Errorf("setting environment variables: %w", err)
	}

	// Also set in the shell's login profile.
	// zsh does not source ~/.profile — it uses ~/.zprofile instead.
	profileFile := ".profile"
	if shell == "zsh" {
		profileFile = ".zprofile"
	}
	profilePath := fmt.Sprintf("/home/%s/%s", username, profileFile)
	var profileLines []string
	for k, v := range env {
		profileLines = append(profileLines, fmt.Sprintf("export %s=%s", k, shellEscape(v)))
	}
	profileContent := strings.Join(profileLines, "\n") + "\n"

	userOpts := incus.ExecOpts{User: 1000, Home: "/home/" + username}
	if _, err := incus.ExecWithStdin(ctx, server, container, userOpts, []string{
		"tee", "-a", profilePath,
	}, []byte(profileContent)); err != nil {
		return fmt.Errorf("setting profile environment variables: %w", err)
	}

	return nil
}

func parsePortSpec(spec string) (containerPort, hostPort int, err error) {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) == 1 {
		// Single port: use same port on both sides.
		containerPort, err = strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid port: %w", err)
		}
		hostPort = containerPort
	} else {
		containerPort, err = strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid container port: %w", err)
		}
		hostPort, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid host port: %w", err)
		}
	}
	if containerPort < 1 || containerPort > 65535 {
		return 0, 0, fmt.Errorf("container port %d out of range 1-65535", containerPort)
	}
	if hostPort < 1 || hostPort > 65535 {
		return 0, 0, fmt.Errorf("host port %d out of range 1-65535", hostPort)
	}
	return containerPort, hostPort, nil
}

func parseMountSpec(spec string) (source, target string, readonly bool, err error) {
	parts := strings.Split(spec, ":")
	if len(parts) < 2 {
		return "", "", false, fmt.Errorf("expected format source:target[:ro]")
	}
	source = parts[0]
	target = parts[1]
	if len(parts) >= 3 && parts[2] == "ro" {
		readonly = true
	}
	return source, target, readonly, nil
}

func status(format string, args ...any) {
	color.Status(format, args...)
}
