package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/gschlager/silo/internal/agents"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/presets"
	"github.com/gschlager/silo/internal/provision"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newInitCmd() *cobra.Command {
	var manual bool
	var agentName string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate a .silo.yml for the current project",
		Long: `Generate a .silo.yml for the current project.

By default, spins up a temporary container with an AI agent that
analyzes the project and generates the configuration interactively.
With --manual, runs a simple scaffolding wizard instead.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			// Check for existing config.
			for _, name := range []string{".silo.yml", ".silo.yaml"} {
				if _, err := os.Stat(filepath.Join(cwd, name)); err == nil {
					return fmt.Errorf("%s already exists in this directory", name)
				}
			}

			if manual {
				return runInteractiveInit(cwd)
			}

			// Resolve agent name from flag or config default.
			if agentName == "" {
				global, err := config.LoadGlobalConfig()
				if err != nil {
					return err
				}
				cfg := config.Merge(global, nil, cwd)
				agentName = cfg.ResolveDefaultAgent()
				if agentName == "" {
					return fmt.Errorf("no agents configured; use --agent to specify one")
				}
			}
			return runAutoInit(cmd.Context(), cwd, agentName)
		},
	}

	cmd.Flags().BoolVarP(&manual, "manual", "m", false, "Use the interactive wizard instead of an AI agent")
	cmd.Flags().StringVar(&agentName, "agent", "", "AI agent to use (default: from global config)")

	return cmd
}

const siloYmlHeader = `# silo project configuration
# https://github.com/gschlager/silo#project-configuration
`

func runAutoInit(ctx context.Context, cwd, agentName string) error {
	global, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	// Build a minimal merged config for the temp container.
	cfg := config.Merge(global, nil, cwd)
	cfg.ContainerName = "silo-init-" + randomSuffix()

	server, err := incus.Connect()
	if err != nil {
		return err
	}

	// Ensure cleanup on exit, including SIGINT. Use a background context
	// for cleanup since the original ctx may be cancelled.
	cleanup := func() {
		cleanupCtx := context.Background()
		color.Status("Removing temporary container...")
		if incus.IsRunning(server, cfg.ContainerName) {
			incus.Stop(cleanupCtx, server, cfg.ContainerName)
		}
		incus.Delete(cleanupCtx, server, cfg.ContainerName)
		agents.CleanupContainerDirs(cfg.ContainerName)
	}
	defer cleanup()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		cleanup()
		os.Exit(1)
	}()
	defer signal.Stop(sigCh)

	// Provision minimal container with just the agent.
	if err := provision.ProvisionMinimal(ctx, server, cfg, agentName); err != nil {
		return err
	}

	// Ensure the agent mode directory exists (empty; never seeded from host).
	agentCfg := cfg.Agents[agentName]
	agents.EnsureModeDir(agentName, agentCfg.Mode)

	// Build env and base command.
	baseCmd := agentCfg.AgentCmd(agentName)
	env := cfg.HostEnv()
	for k, v := range agentCfg.Env {
		env[k] = v
	}
	opts := incus.UserOpts(cfg.UserHome(), cfg.WorkspacePath())
	opts.Env = env

	// Step 1: Run the agent non-interactively to generate a draft .silo.yml.
	prompt := autoInitPrompt(cfg)
	configPath := filepath.Join(cwd, ".silo.yml")

	generateConfig := func() error {
		color.Status("Generating .silo.yml with %s (this may take a minute)...", agentName)
		fmt.Println()
		genCmd := baseCmd + " -p " + shellQuote([]string{prompt})
		return incus.ExecStreaming(ctx, server, cfg.ContainerName, opts,
			cfg.LoginCmd("cd "+cfg.WorkspacePath()+" && "+genCmd),
			os.Stdout, os.Stderr)
	}

	if err := generateConfig(); err != nil {
		if _, statErr := os.Stat(configPath); statErr != nil {
			// Generation failed and no config — likely needs login.
			fmt.Println()
			color.Info("%s needs to be logged in first.", agentName)
			color.Info("Please log in, then exit the session (Ctrl+C or /exit) to continue.")
			fmt.Println()
			fmt.Fprintf(os.Stderr, "Press Enter to open %s...", agentName)
			fmt.Scanln()

			if err := incus.ExecInteractive(ctx, server, cfg.ContainerName, opts,
				cfg.LoginCmd("cd "+cfg.WorkspacePath()+" && "+baseCmd)); err != nil {
				// Ignore — user exited after login.
			}

			// Credentials are live-mounted, no sync needed.

			// Retry generation.
			fmt.Println()
			if err := generateConfig(); err != nil {
				color.Warn("agent exited: %v", err)
			}
		}
	}

	// Step 2: Show the generated config and offer to refine it.
	if _, err := os.Stat(configPath); err == nil {
		fmt.Println()
		color.Success("Generated .silo.yml:")
		fmt.Println()

		if data, err := os.ReadFile(configPath); err == nil {
			highlightYAML(string(data))
		}

		fmt.Println()
		color.Info("Things to consider:")
		color.Info("  - Are all required services listed (database, Redis, etc.)?")
		color.Info("  - Are the port forwards correct for your host-side tools?")
		color.Info("  - Are daemon commands correct for development mode?")
		fmt.Println()

		fmt.Fprintf(os.Stderr, "Refine this config with %s? [y/N] ", agentName)
		var answer string
		fmt.Scanln(&answer)
		if strings.HasPrefix(strings.ToLower(answer), "y") {
			fmt.Println()
			if err := incus.ExecInteractive(ctx, server, cfg.ContainerName, opts,
				cfg.LoginCmd("cd "+cfg.WorkspacePath()+" && "+baseCmd)); err != nil {
				color.Warn("agent exited: %v", err)
			}
		}
	} else {
		color.Warn("No .silo.yml was generated.")
	}

	// Final check.
	if _, err := os.Stat(configPath); err == nil {
		color.Success("Generated .silo.yml")
		color.Info("Run 'silo up' to start the environment.")
	} else {
		color.Warn("No .silo.yml was generated.")
	}

	return nil
}

func runInteractiveInit(cwd string) error {
	global, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	cfg := config.ProjectConfig{}

	fmt.Printf("Project directory: %s\n", cwd)
	fmt.Printf("Container name:    %s\n", config.ContainerName(cwd))
	fmt.Println()

	// Detect image.
	fmt.Printf("Base image [%s]: ", global.DefaultImage)
	var image string
	fmt.Scanln(&image)
	if image != "" && image != global.DefaultImage {
		cfg.Image = image
	}

	// Language runtimes via presets.
	fmt.Print("\nRuby versions (comma-separated, e.g. 3.4 or 3.3,3.4,jruby) or empty: ")
	var rubyLine string
	fmt.Scanln(&rubyLine)
	if rubyLine = strings.TrimSpace(rubyLine); rubyLine != "" {
		var versions []string
		for _, v := range strings.Split(rubyLine, ",") {
			if v = strings.TrimSpace(v); v != "" {
				versions = append(versions, v)
			}
		}
		var node yaml.Node
		if err := node.Encode(map[string]any{"versions": versions}); err != nil {
			return err
		}
		cfg.Use = append(cfg.Use, config.PresetUse{Name: "ruby", Params: node})
	}

	fmt.Print("Install Node.js? [y/N]: ")
	var nodeAns string
	fmt.Scanln(&nodeAns)
	if strings.HasPrefix(strings.ToLower(nodeAns), "y") {
		cfg.Use = append(cfg.Use, config.PresetUse{Name: "node"})
	}

	// Detect private remotes — the PAT goes in the central secrets file, not .silo.yml.
	if hasPrivateRemote(cwd) {
		project := config.ProjectName(cwd)
		fmt.Println("\nPrivate remote detected. The GitHub PAT goes in the central secrets file for push/pull.")
		fmt.Println("Recommended: Create a fine-grained PAT at GitHub > Settings > Developer Settings > Fine-grained tokens")
		fmt.Print("1Password reference (op://...) or leave empty to add a stub: ")
		var ref string
		fmt.Scanln(&ref)
		if ref = strings.TrimSpace(ref); ref != "" {
			if added, err := config.AddProjectSecret(project, "github", ref); err != nil {
				color.Warn("could not update secrets file: %v", err)
			} else if added {
				fmt.Printf("Added a github PAT for %q to %s\n", project, config.SecretsPath())
			} else {
				color.Warn("%q already has an entry in %s — add the github key manually", project, config.SecretsPath())
			}
		} else if _, err := config.EnsureSecretsStub(project); err == nil {
			fmt.Printf("Added a secrets stub for %q to %s\n", project, config.SecretsPath())
		}
	}

	// Ports.
	fmt.Print("\nPort forwards (e.g., 5432:15432,3000:13000) or empty: ")
	reader := bufio.NewReader(os.Stdin)
	ports, _ := reader.ReadString('\n')
	ports = strings.TrimSpace(ports)
	if ports != "" {
		for _, p := range strings.Split(ports, ",") {
			cfg.Ports = append(cfg.Ports, config.PortForward{Spec: strings.TrimSpace(p)})
		}
	}

	// Write .silo.yml.
	data, err := config.MarshalYAML(&cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	data = append([]byte(siloYmlHeader), data...)

	path := filepath.Join(cwd, ".silo.yml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	fmt.Printf("\nCreated %s\n", path)
	fmt.Println("Edit it to add setup, sync, and other commands, then run 'silo up'.")
	return nil
}

func autoInitPrompt(cfg *config.MergedConfig) string {
	wsPath := cfg.WorkspacePath()
	prompt := fmt.Sprintf(`You are helping generate a .silo.yml configuration file for a project.

.silo.yml configures an isolated development environment using Incus system containers.
Analyze the project in %s and generate a complete .silo.yml.`, wsPath) + `

The file format (all fields are optional):

  image: fedora/44          # Base image (default: fedora/44)
  use:                      # Built-in presets — prefer these over installing runtimes by hand
    ruby:                   #   Ruby via rv; jruby/truffleruby are valid version entries
      versions: ["3.4"]
      default: "3.4"
    node:                   #   Node.js + corepack (pnpm/yarn via the packageManager field)
  setup:                    # Commands run once on first provisioning (as dev user with sudo)
    - sudo dnf install -y postgresql16-server redis
    - sudo systemctl enable --now postgresql redis
    - bundle install
    - bin/rails db:create
  sync:                     # Commands after code changes (e.g. after git pull)
    - bundle install
    - bin/rails db:migrate
  reset:                    # Named reset targets
    db:
      - bin/rails db:reset
  update:                   # System-level updates
    - sudo dnf update -y
  ports:                    # Port forwards (container:host)
    - 5432:15432
    - 3000:13000
  env:                      # Environment variables
    RAILS_ENV: development
  daemons:                  # Long-running processes (managed as systemd user services)
    rails:
      cmd: bin/rails server -b 0.0.0.0
      ports: ["3000"]
    sidekiq:
      cmd: bundle exec sidekiq
      autostart: false
      after: rails            # starts after rails (systemd dependency)
  nesting: false            # Enable container nesting (Docker, Podman, etc.)

Important rules:
- setup commands run as the dev user with a login shell. Use "sudo" for commands that need root (dnf, systemctl, etc.)
- Prefer use: presets for language runtimes instead of installing them by hand. Available presets: ` + strings.Join(presets.Available(), ", ") + `
  - For ruby, set versions from .ruby-version or the Gemfile ruby directive; jruby/truffleruby are valid entries. Omit the ruby preset if the project is not Ruby.
  - The login shell is bash. Do NOT write shell-init/activation lines (no ~/.zshrc, ~/.zshenv, ~/.profile, ~/.zprofile, no "mise activate"/"rv shell init" appended to dotfiles). Presets wire runtime activation. If another tool genuinely needs activation, append one POSIX-sh line to ~/.silo/env.sh.
- Do NOT put tokens, PATs, or secrets in .silo.yml. Per-project secrets live in ~/.config/silo/secrets.yml and are injected as environment variables (e.g. GITHUB_TOKEN); just assume they are present.
- sync should be incremental (fast) — not a full rebuild
- Look at the project files to determine: language, package manager, services needed, ports
- Check for Dockerfile, docker-compose.yml, Gemfile, package.json, go.mod, requirements.txt, etc.
- Write the file to ` + wsPath + `/.silo.yml
- Start the file with these comment lines:
  # silo project configuration
  # https://github.com/gschlager/silo#project-configuration
`

	// Add environment context.
	prompt += fmt.Sprintf(`
Environment context:
- Base image: %s
- The following packages are pre-installed via default_setup: %s
`, cfg.Image, strings.Join(cfg.DefaultSetup, "; "))

	prompt += `
Important: The .silo.yml must be portable — do not reference host-specific
paths or assume anything is pre-installed beyond the default_setup packages.
All dependencies should be installed from scratch in the setup commands.
The container starts clean each time.

Analyze the project now and generate the configuration.`
	return prompt
}

func hasPrivateRemote(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "remote", "-v")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	output := string(out)
	return strings.Contains(output, "git@") || strings.Contains(output, "private")
}

func randomSuffix() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

