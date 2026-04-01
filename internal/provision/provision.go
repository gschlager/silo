package provision

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/agents"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
)

// Provision runs the full first-run provisioning flow for a container.
func Provision(server incuscli.InstanceServer, cfg *config.MergedConfig, verbose bool) error {
	verboseOutput = verbose
	name := cfg.ContainerName

	// Step 1: Create and start the container.
	status("Creating container %s from %s...", name, cfg.Image)
	if err := incus.Launch(server, cfg.Image, name); err != nil {
		return err
	}

	// Step 2: Wait for network.
	status("Waiting for network...")
	if err := incus.WaitForNetwork(server, name, 30*time.Second); err != nil {
		return err
	}

	// Step 3: Docker nesting (must be set before services start).
	if cfg.Docker {
		status("Enabling container nesting...")
		if err := incus.SetConfig(server, name, "security.nesting", "true"); err != nil {
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
		if err := incus.AddProxyDevice(server, name, deviceName, hostPort, containerPort); err != nil {
			return err
		}
	}

	// Step 4: Mount the project directory.
	status("Mounting project directory...")
	if err := incus.AddDiskDevice(server, name, "workspace", cfg.ProjectDir, "/workspace", false); err != nil {
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
		if err := incus.AddDiskDevice(server, name, deviceName, source, target, readonly); err != nil {
			return err
		}
	}

	// Step 6: Run default_setup (as root, before user creation so shell is available).
	if len(cfg.DefaultSetup) > 0 {
		status("Running default setup...")
		if err := runCommands(server, name, incus.ExecOpts{}, cfg.DefaultSetup); err != nil {
			return fmt.Errorf("default_setup failed: %w", err)
		}
	}

	// Step 7: Create dev user.
	status("Creating user %s...", cfg.User)
	if err := CreateUser(server, name, cfg.User, cfg.Shell); err != nil {
		return err
	}

	// Step 8: Configure git.
	if len(cfg.Git) > 0 {
		status("Configuring git...")
		if err := ConfigureGit(server, name, cfg.User, cfg.Git); err != nil {
			return err
		}
	}

	// Step 9: Set up git credential helper.
	if cfg.GitCredential != nil {
		status("Setting up git credentials...")
		if err := SetupCredentialHelper(server, name, cfg.User, cfg.GitCredential); err != nil {
			return err
		}
	}

	// Step 10: Set up tools.
	if err := setupTools(server, name, cfg); err != nil {
		return err
	}

	// Step 11: Install agents.
	if len(cfg.Agents) > 0 {
		if err := agents.InstallAgents(server, name, cfg.User, cfg.Shell, cfg.Agents); err != nil {
			return err
		}
	}

	// Step 12: Set up agent data directories.
	if len(cfg.Agents) > 0 {
		status("Setting up agent directories...")
		if err := agents.SetupAgentDirs(server, name, cfg.ContainerName, cfg.Agents); err != nil {
			return err
		}
	}

	// Step 12: Set timezone and locale.
	status("Configuring timezone and locale...")
	if err := configureTimezoneAndLocale(server, name); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not set timezone/locale: %v\n", err)
	}

	// Step 13: Set environment variables.
	if len(cfg.Env) > 0 {
		status("Setting environment variables...")
		if err := setEnvironment(server, name, cfg.User, cfg.Env); err != nil {
			return err
		}
	}

	// Step 14: Run project setup (as dev user).
	if len(cfg.Setup) > 0 {
		status("Running project setup...")
		userOpts := incus.UserOpts("/home/"+cfg.User, "/workspace")
		if err := runCommands(server, name, userOpts, cfg.Setup); err != nil {
			return fmt.Errorf("setup failed: %w", err)
		}
	}

	// Step 15: Set up daemons.
	if len(cfg.Daemons) > 0 {
		status("Setting up daemons...")
		if err := SetupDaemons(server, name, cfg.User, cfg.Daemons); err != nil {
			return err
		}
	}

	// Step 16: Set up notifications.
	if cfg.Notifications {
		status("Setting up notifications...")
		if err := SetupNotifications(server, name, cfg.User); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not set up notifications: %v\n", err)
		}
	}

	// Step 17: Mark as initialized.
	rootOpts := incus.ExecOpts{}
	if _, err := incus.Exec(server, name, rootOpts, []string{
		"sh", "-c", fmt.Sprintf("touch /home/%s/.silo-initialized", cfg.User),
	}); err != nil {
		return fmt.Errorf("creating init marker: %w", err)
	}

	// Step 18: Take initial snapshot.
	status("Taking initial snapshot...")
	if err := incus.CreateSnapshot(server, name, "initial"); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: could not create initial snapshot: %v\n", err)
	}

	status("Environment ready!")
	return nil
}

// IsInitialized checks if a container has been fully provisioned.
func IsInitialized(server incuscli.InstanceServer, container, username string) bool {
	_, err := incus.Exec(server, container, incus.ExecOpts{}, []string{
		"test", "-f", fmt.Sprintf("/home/%s/.silo-initialized", username),
	})
	return err == nil
}

var verboseOutput bool

func runCommands(server incuscli.InstanceServer, container string, opts incus.ExecOpts, commands []string) error {
	for _, cmd := range commands {
		if verboseOutput {
			fmt.Fprintf(os.Stderr, "  $ %s\n", cmd)
			if err := incus.ExecStreaming(server, container, opts, []string{"sh", "-c", cmd}, os.Stdout, os.Stderr); err != nil {
				return fmt.Errorf("command %q: %w", cmd, err)
			}
		} else {
			if _, err := incus.Exec(server, container, opts, []string{"sh", "-c", cmd}); err != nil {
				return fmt.Errorf("command %q: %w", cmd, err)
			}
		}
	}
	return nil
}

func setupTools(server incuscli.InstanceServer, container string, cfg *config.MergedConfig) error {
	for name, tool := range cfg.Tools {
		if tool.Credential == nil {
			continue
		}
		status("Setting up tool %q...", name)

		token, err := resolveCredential(tool.Credential)
		if err != nil {
			return fmt.Errorf("resolving credential for tool %q: %w", name, err)
		}

		// Set the appropriate environment variable based on tool name.
		envVar := ""
		switch name {
		case "gh":
			envVar = "GH_TOKEN"
		default:
			envVar = strings.ToUpper(name) + "_TOKEN"
		}

		if envVar != "" {
			env := map[string]string{envVar: token}
			if err := setEnvironment(server, container, cfg.User, env); err != nil {
				return fmt.Errorf("setting env for tool %q: %w", name, err)
			}
		}
	}
	return nil
}

func configureTimezoneAndLocale(server incuscli.InstanceServer, container string) error {
	rootOpts := incus.ExecOpts{}

	// Copy host timezone.
	tz, err := os.ReadFile("/etc/timezone")
	if err == nil {
		tzName := strings.TrimSpace(string(tz))
		incus.Exec(server, container, rootOpts, []string{
			"ln", "-sf", "/usr/share/zoneinfo/" + tzName, "/etc/localtime",
		})
	} else {
		// Try reading from timedatectl or /etc/localtime symlink.
		if target, err := os.Readlink("/etc/localtime"); err == nil {
			incus.Exec(server, container, rootOpts, []string{
				"ln", "-sf", target, "/etc/localtime",
			})
		}
	}

	// Copy host locale.
	lang := os.Getenv("LANG")
	if lang != "" {
		incus.Exec(server, container, rootOpts, []string{
			"sh", "-c", fmt.Sprintf(`echo 'LANG=%s' > /etc/locale.conf`, lang),
		})
	}

	return nil
}

func setEnvironment(server incuscli.InstanceServer, container, username string, env map[string]string) error {
	rootOpts := incus.ExecOpts{}

	// Write environment variables to /etc/environment.d/ for system-wide availability.
	var lines []string
	for k, v := range env {
		lines = append(lines, fmt.Sprintf("%s=%s", k, v))
	}
	content := strings.Join(lines, "\n") + "\n"

	if _, err := incus.Exec(server, container, rootOpts, []string{
		"sh", "-c", fmt.Sprintf(
			`mkdir -p /etc/environment.d && cat >> /etc/environment.d/silo.conf << 'EOF'
%sEOF`, content),
	}); err != nil {
		return fmt.Errorf("setting environment variables: %w", err)
	}

	// Also set in ~/.profile for login shell sessions.
	for k, v := range env {
		incus.Exec(server, container, rootOpts, []string{
			"su", "-", username, "-c",
			fmt.Sprintf(`echo 'export %s="%s"' >> ~/.profile`, k, v),
		})
	}

	return nil
}

func parsePortSpec(spec string) (containerPort, hostPort int, err error) {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected format container_port:host_port")
	}
	containerPort, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid container port: %w", err)
	}
	hostPort, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid host port: %w", err)
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
	fmt.Fprintf(os.Stderr, "==> "+format+"\n", args...)
}
