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
func ProvisionMinimal(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig, agentName string) (err error) {
	name := cfg.ContainerName

	// Buffer command output in non-verbose mode and replay it if anything fails.
	color.StartCapture()
	defer func() {
		if err != nil {
			color.DumpCapture()
		}
		color.StopCapture()
	}()

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

	// Ensure the configured login shell exists before creating the user.
	effectiveShell, err := EnsureShell(ctx, server, name, cfg.Shell)
	if err != nil {
		return err
	}
	cfg.Shell = effectiveShell

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
		if err := agents.InstallAgents(ctx, server, name, cfg.User, cfg.Shell, singleAgent); err != nil {
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

// Provision runs the full first-run provisioning flow for a container. When
// keepOnFailure is set, a failed provision leaves the container in place (still
// running) so it can be inspected instead of being torn down.
func Provision(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig, keepOnFailure bool) error {
	name := cfg.ContainerName

	// Buffer provisioning command output in non-verbose mode so it can be
	// replayed if a step fails — otherwise the user sees only the final error.
	color.StartCapture()
	defer color.StopCapture()

	// Step 1: Create and start the container.
	status("Creating container %s from %s...", name, cfg.Image)
	if err := incus.Launch(ctx, server, cfg.Image, name); err != nil {
		return err
	}

	// Clean up the container if provisioning fails.
	success := false
	defer func() {
		if !success {
			color.DumpCapture()
			if keepOnFailure {
				color.Warn("Provisioning failed. Keeping container %s for inspection (--keep); enter it with `silo enter` or remove it with `silo rm`.", name)
				return
			}
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
	if len(cfg.Ports) > 0 {
		status("Setting up port forwards...")
		if err := ReconcilePorts(ctx, server, name, cfg.Ports); err != nil {
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

	// Step 6b: Ensure the configured login shell exists (install it if missing,
	// fall back to bash). Decoupled from default_setup so it runs for every image
	// — silo execs every command through this shell. Persist the effective shell
	// so later commands agree even when it differs from the configured one.
	effectiveShell, err := EnsureShell(ctx, server, name, cfg.Shell)
	if err != nil {
		return err
	}
	cfg.Shell = effectiveShell
	if err := config.SaveContainerShell(name, effectiveShell); err != nil {
		color.Warn("could not record effective shell: %v", err)
	}

	// Step 7: Create dev user.
	status("Creating user %s...", cfg.User)
	if err := CreateUser(ctx, server, name, cfg.User, cfg.Shell); err != nil {
		return err
	}

	// Step 7b: Enable prefix history search (Up/Down) for bash line editing.
	if err := configureInputrc(ctx, server, name); err != nil {
		color.Warn("could not configure inputrc: %v", err)
	}

	// Step 8: Configure git.
	if len(cfg.Git) > 0 {
		status("Configuring git...")
		if err := ConfigureGit(ctx, server, name, cfg.User, cfg.Git); err != nil {
			return err
		}
	}

	// Step 8b: Apply the global gitignore (git's per-user excludes file).
	if err := ApplyGitignore(ctx, server, name, cfg.User); err != nil {
		color.Warn("could not apply global gitignore: %v", err)
	}

	// Step 9: Install the GitHub credential helper when a github token source is
	// configured — either a project git.credential or a central secrets 'github'
	// key. The helper reads $GITHUB_TOKEN at runtime, so nothing is baked in.
	needHelper := cfg.GitCredential != nil
	if !needHelper {
		if secrets, err := config.SecretsForProject(cfg.ProjectName()); err == nil {
			_, needHelper = secrets["github"]
		}
	}
	if needHelper {
		status("Setting up git credentials...")
		if err := InstallGitHubCredentialHelper(ctx, server, name, cfg.User); err != nil {
			return err
		}
	}

	// Step 10: Resolve tool credentials (passed at runtime, not baked in).

	// Step 11: Install agents.
	if len(cfg.Agents) > 0 {
		if err := agents.InstallAgents(ctx, server, name, cfg.User, cfg.Shell, cfg.Agents); err != nil {
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
		if err := setEnvironment(ctx, server, name, cfg.User, cfg.Env); err != nil {
			return err
		}
	}

	// Step 14: Run project setup (as dev user with login shell).
	if len(cfg.Setup) > 0 {
		status("Running project setup...")
		userOpts := incus.UserOpts("/home/"+cfg.User, cfg.WorkspacePath())
		// Make credentials (GITHUB_TOKEN, etc.) available to setup so it can fetch
		// private dependencies. Resolved on the host; never written to disk.
		credEnv, err := ResolveSessionEnv(cfg)
		if err != nil {
			return fmt.Errorf("resolving setup credentials: %w", err)
		}
		userOpts.Env = credEnv
		if err := runUserCommands(ctx, server, name, userOpts, cfg.Shell, cfg.Setup); err != nil {
			return fmt.Errorf("setup failed: %w", err)
		}
	}

	// Step 15: Set up daemons. Install the unit files, then start the autostart
	// ones ourselves (with env: and secrets injected) rather than enabling them
	// for boot.
	if len(cfg.Daemons) > 0 {
		status("Setting up daemons...")
		if err := ReconcileDaemons(ctx, server, name, cfg.User, cfg.Shell, cfg.WorkspacePath(), cfg.Daemons); err != nil {
			return err
		}
		if err := StartConfiguredDaemons(ctx, server, cfg); err != nil {
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

// runUserCommands runs commands as the dev user in a single login shell
// session, so PATH/env mutations (export, eval'd shell init, cd) carry over
// from one command to the next. Aborts on the first failure (`set -e`).
//
// Output streams to color.Detail* — live with --verbose, otherwise buffered for
// replay if the command fails.
func runUserCommands(ctx context.Context, server incuscli.InstanceServer, container string, opts incus.ExecOpts, shell string, commands []string) error {
	if len(commands) == 0 {
		return nil
	}
	loginShell := "/bin/" + shell

	var script strings.Builder
	script.WriteString("set -e\n")
	for _, cmd := range commands {
		// Print a marker so users can see which command is running.
		script.WriteString("printf '\\n\\033[1;36m→\\033[0m %s\\n' ")
		script.WriteString(shellEscape(cmd))
		script.WriteString("\n")
		script.WriteString(cmd)
		script.WriteString("\n")
	}

	fullCmd := []string{loginShell, "-lc", script.String()}
	return incus.ExecStreaming(ctx, server, container, opts, fullCmd, color.DetailOut(), color.DetailErr())
}

func runCommands(ctx context.Context, server incuscli.InstanceServer, container string, opts incus.ExecOpts, commands []string) error {
	for _, cmd := range commands {
		color.Command(cmd)
		if err := incus.ExecStreaming(ctx, server, container, opts, []string{"sh", "-c", cmd}, color.DetailOut(), color.DetailErr()); err != nil {
			return fmt.Errorf("command %q: %w", cmd, err)
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

func setEnvironment(ctx context.Context, server incuscli.InstanceServer, container, username string, env map[string]string) error {
	rootOpts := incus.ExecOpts{}

	// Write environment variables to /etc/environment.d/ for systemd user units.
	// This file is static — systemd does no shell expansion — so a value that
	// references another variable (e.g. PATH="dir:$PATH") would land verbatim and,
	// for PATH, drop the system dirs from anything that picks it up (sudo, rpm
	// scriptlets). Such values are handled only in env.sh below, where the shell
	// expands them; here we write just the plain ones.
	var lines []string
	for k, v := range env {
		if strings.ContainsRune(v, '$') {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s=%s", k, v))
	}
	if len(lines) > 0 {
		// Ensure directory exists (no user-controlled data in this command).
		if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
			"mkdir", "-p", "/etc/environment.d",
		}); err != nil {
			return fmt.Errorf("creating environment.d: %w", err)
		}
		// Pipe the content via stdin to avoid any shell interpretation.
		content := strings.Join(lines, "\n") + "\n"
		if _, err := incus.ExecWithStdin(ctx, server, container, rootOpts, []string{
			"tee", "-a", "/etc/environment.d/silo.conf",
		}, []byte(content)); err != nil {
			return fmt.Errorf("setting environment variables: %w", err)
		}
	}

	// Also export from the shell-neutral activation file so interactive shells,
	// `silo run`, and daemons all see the variables regardless of shell. Each
	// export is skipped when the variable is already set: env.sh is re-sourced
	// by every non-interactive bash via BASH_ENV, and a forced export would
	// clobber overrides like `RAILS_ENV=test ...` in child shells (and the
	// per-agent env passed at exec time).
	var profileLines []string
	for k, v := range env {
		// PATH is a prepend, not an assignment: expand $PATH (env.sh is sourced
		// after the system PATH is set) and dedup-guard so the BASH_ENV re-source
		// doesn't stack duplicates. The existence guard used for plain vars would
		// skip it outright, since PATH is always already set.
		if k == "PATH" {
			profileLines = append(profileLines, pathPrependLine(v))
			continue
		}
		profileLines = append(profileLines, fmt.Sprintf(`[ -n "${%s+x}" ] || export %s=%s`, k, k, shellEscape(v)))
	}
	profileContent := strings.Join(profileLines, "\n") + "\n"

	if err := appendActivation(ctx, server, container, username, profileContent); err != nil {
		return fmt.Errorf("setting profile environment variables: %w", err)
	}

	return nil
}

// pathPrependLine builds the env.sh line that applies a user-provided PATH
// value (typically "dir:dir:$PATH"). The value is emitted double-quoted so
// $PATH expands, guarded on its first segment so re-sourcing env.sh via
// BASH_ENV doesn't stack duplicates — mirroring the ~/.local/bin seed.
func pathPrependLine(value string) string {
	first := value
	if i := strings.IndexByte(value, ':'); i >= 0 {
		first = value[:i]
	}
	// A leading variable reference (or empty first segment) can't be matched
	// against $PATH, so skip the dedup guard and just export.
	if first == "" || strings.HasPrefix(first, "$") {
		return fmt.Sprintf(`export PATH="%s"`, value)
	}
	return fmt.Sprintf(`case ":$PATH:" in *":%s:"*) ;; *) export PATH="%s" ;; esac`, first, value)
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
