package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gschlager/silo/internal/agents"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	"github.com/spf13/cobra"
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
			return runAutoInit(cmd.Context(), cwd, agentName)
		},
	}

	cmd.Flags().BoolVarP(&manual, "manual", "m", false, "Use the interactive wizard instead of an AI agent")
	cmd.Flags().StringVar(&agentName, "agent", "claude", "AI agent to use for config generation")

	return cmd
}

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

	// Ensure cleanup on exit.
	defer func() {
		color.Status("Removing temporary container...")
		if incus.IsRunning(server, cfg.ContainerName) {
			incus.Stop(ctx, server, cfg.ContainerName)
		}
		incus.Delete(ctx, server, cfg.ContainerName)
	}()

	// Provision minimal container with just the agent.
	if err := provision.ProvisionMinimal(ctx, server, cfg, agentName); err != nil {
		return err
	}

	// Refresh agent credentials.
	color.Status("Refreshing %s credentials...", agentName)
	if err := agents.RefreshAlwaysSeeds(cfg.ContainerName, cfg.Agents); err != nil {
		color.Warn("could not refresh seeds: %v", err)
	}

	// Build the agent prompt.
	prompt := autoInitPrompt()

	// Build agent command and env.
	agentCfg := cfg.Agents[agentName]
	env := make(map[string]string)
	for k, v := range agentCfg.Env {
		env[k] = v
	}

	agentCmd := shellQuote([]string{agentName, "-p", prompt})
	shellCmd := "cd /workspace && " + agentCmd

	// Launch agent interactively.
	fmt.Print("\033[2J\033[H")
	opts := incus.UserOpts(cfg.UserHome(), "/workspace")
	opts.Env = env
	if err := incus.ExecInteractive(ctx, server, cfg.ContainerName, opts, cfg.LoginCmd(shellCmd)); err != nil {
		color.Warn("agent exited: %v", err)
	}

	// Check if config was generated.
	configPath := filepath.Join(cwd, ".silo.yml")
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

	// Detect private remotes.
	if hasPrivateRemote(cwd) {
		fmt.Println("\nPrivate remote detected. You'll need a git credential for push/pull.")
		fmt.Println("Recommended: Create a fine-grained PAT at GitHub > Settings > Developer Settings > Fine-grained tokens")
		fmt.Print("1Password reference (op://...) or leave empty to skip: ")
		var ref string
		fmt.Scanln(&ref)
		if ref != "" {
			cfg.Git.Credential = &config.CredentialConfig{
				Source: "1password",
				Ref:    ref,
			}
		}
	}

	// Ports.
	fmt.Print("\nPort forwards (e.g., 5432:15432,3000:13000) or empty: ")
	reader := bufio.NewReader(os.Stdin)
	ports, _ := reader.ReadString('\n')
	ports = strings.TrimSpace(ports)
	if ports != "" {
		for _, p := range strings.Split(ports, ",") {
			cfg.Ports = append(cfg.Ports, strings.TrimSpace(p))
		}
	}

	// Write .silo.yml.
	data, err := config.MarshalYAML(&cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	path := filepath.Join(cwd, ".silo.yml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	fmt.Printf("\nCreated %s\n", path)
	fmt.Println("Edit it to add setup, sync, and other commands, then run 'silo up'.")
	return nil
}

func autoInitPrompt() string {
	return `You are helping generate a .silo.yml configuration file for a project.

.silo.yml configures an isolated development environment using Incus system containers.
Analyze the project in /workspace and generate a complete .silo.yml.

The file format (all fields are optional):

  image: fedora/43          # Base image (default: fedora/43)
  setup:                    # Commands run once on first provisioning (as dev user with sudo)
    - sudo dnf install -y postgresql16-server redis ruby nodejs
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
    rails: bin/rails server -b 0.0.0.0
    sidekiq:
      cmd: bundle exec sidekiq
      autostart: false
  docker: false             # Enable nested Docker
  compose: ""               # Docker compose file to start on silo up

Important rules:
- setup commands run as the dev user. Use "sudo" for commands that need root (dnf, systemctl, etc.)
- sync should be incremental (fast) — not a full rebuild
- Look at the project files to determine: language, package manager, services needed, ports
- Check for Dockerfile, docker-compose.yml, Gemfile, package.json, go.mod, requirements.txt, etc.
- Write the file to /workspace/.silo.yml

Analyze the project now and generate the configuration. Discuss your choices with the user and iterate until they're satisfied.`
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
