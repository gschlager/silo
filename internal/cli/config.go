package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/gschlager/silo/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
	}

	cmd.AddCommand(
		newConfigEditCmd(),
		newConfigShowCmd(),
		newConfigPathCmd(),
		newConfigMigrateSecretsCmd(),
	)
	return cmd
}

func newConfigMigrateSecretsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate-secrets",
		Short: "Restore this project's readable secrets key",
		Long: `Rename the current project's temporary hash-suffixed key in secrets.yml
to its registry-assigned readable key. The migration is atomic and idempotent.
If both keys contain values, no changes are made so they can be merged manually.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting working directory: %w", err)
			}
			containerName, err := config.AssignContainerName(cwd)
			if err != nil {
				return err
			}
			currentKey := strings.TrimPrefix(containerName, "silo-")
			pathKey := config.PathScopedProjectName(cwd)
			result, err := config.MigrateSecretsProjectKey(pathKey, currentKey)
			if err != nil {
				return err
			}
			switch result {
			case config.SecretsMigrated:
				fmt.Printf("Migrated secrets key %q to %q in %s\n", pathKey, currentKey, config.SecretsPath())
			case config.SecretsAlreadyCurrent:
				fmt.Printf("Secrets already use %q; nothing to migrate.\n", currentKey)
			case config.SecretsLegacyMissing:
				fmt.Printf("No path-scoped secrets key %q found in %s; nothing to migrate.\n", pathKey, config.SecretsPath())
			}
			return nil
		},
	}
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open global config in $EDITOR",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.GlobalConfigPath()

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = os.Getenv("VISUAL")
			}
			if editor == "" {
				return fmt.Errorf("$EDITOR is not set; edit %s manually", path)
			}

			editorCmd := exec.Command(editor, path)
			editorCmd.Stdin = os.Stdin
			editorCmd.Stdout = os.Stdout
			editorCmd.Stderr = os.Stderr
			return editorCmd.Run()
		},
	}
}

// resolvedView is the merged, fully-resolved configuration for the current
// project, rendered by `silo config show`. Field order is the display order.
type resolvedView struct {
	Image   string              `yaml:"image"`
	Shell   string              `yaml:"shell"`
	User    string              `yaml:"user"`
	Setup   []string            `yaml:"setup,omitempty"`
	Sync    []string            `yaml:"sync,omitempty"`
	Reset   map[string][]string `yaml:"reset,omitempty"`
	Update  []string            `yaml:"update,omitempty"`
	Ports   []string            `yaml:"ports,omitempty"`
	Env     map[string]string   `yaml:"env,omitempty"`
	Git     map[string]string   `yaml:"git,omitempty"`
	Secrets map[string]string   `yaml:"secrets,omitempty"`
	Daemons []string            `yaml:"daemons,omitempty"`
	// DaemonEnv lists, per daemon, the names of the variables its own env:
	// block injects. Names only — the values are literals or op:// references
	// that may be secrets, so `config show` never resolves or prints them.
	DaemonEnv map[string][]string `yaml:"daemon_env,omitempty"`
	Agents    []string            `yaml:"agents,omitempty"`
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the resolved configuration for the current project",
		Long: `Show what silo will actually do for the current project: the merged
global + project config with presets expanded into setup, plus the secrets that
apply (by reference only — tokens are never resolved or printed).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			view := resolvedView{
				Image:  cfg.Image,
				Shell:  cfg.Shell,
				User:   cfg.User,
				Setup:  cfg.Setup,
				Sync:   cfg.Sync,
				Reset:  cfg.Reset,
				Update: cfg.Update,
				Env:    cfg.Env,
				Git:    cfg.Git,
			}
			for _, p := range cfg.Ports {
				view.Ports = append(view.Ports, p.Spec)
			}
			for name, d := range cfg.Daemons {
				view.Daemons = append(view.Daemons, name)
				if len(d.Env) == 0 {
					continue
				}
				keys := make([]string, 0, len(d.Env))
				for k := range d.Env {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				if view.DaemonEnv == nil {
					view.DaemonEnv = make(map[string][]string)
				}
				view.DaemonEnv[name] = keys
			}
			sort.Strings(view.Daemons)
			for _, name := range cfg.AgentOrder {
				if a, ok := cfg.Agents[name]; ok && a.Enabled {
					view.Agents = append(view.Agents, name)
				}
			}
			// Preset secrets first, so a project entry of the same name displays
			// as the one that actually wins — matching ResolveSessionEnv.
			for k, v := range cfg.PresetSecrets {
				if view.Secrets == nil {
					view.Secrets = make(map[string]string)
				}
				view.Secrets[k] = maskSecret(v)
			}
			if secrets, err := config.SecretsForProjects(cfg.ProjectName(), cfg.PathScopedProjectName()); err == nil && len(secrets) > 0 {
				if view.Secrets == nil {
					view.Secrets = make(map[string]string, len(secrets))
				}
				for k, v := range secrets {
					view.Secrets[k] = maskSecret(v)
				}
			}

			data, err := config.MarshalYAML(&view)
			if err != nil {
				return err
			}
			header := fmt.Sprintf("# Resolved configuration for %s (container %s)\n", cfg.ProjectName(), cfg.ContainerName)
			return highlightYAML(header + string(data))
		},
	}
}

// maskSecret shows 1Password references (which are pointers, not secrets) as-is
// and hides literal values so `config show` never prints a token.
func maskSecret(value string) string {
	if strings.HasPrefix(value, "op://") {
		return value
	}
	return "<hidden literal>"
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the global config file path",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(config.GlobalConfigPath())
		},
	}
}

func highlightYAML(src string) error {
	var buf bytes.Buffer
	err := quick.Highlight(&buf, src, "yaml", "terminal256", "monokai")
	if err != nil {
		// Fallback to plain output.
		fmt.Print(src)
		return nil
	}
	fmt.Print(buf.String())
	return nil
}
