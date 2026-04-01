package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gschlager/silo/internal/config"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Interactive scaffolding of .silo.yml for the current project",
		Args:  cobra.NoArgs,
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
			var ports string
			fmt.Scanln(&ports)
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
		},
	}
}

func hasPrivateRemote(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "remote", "-v")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// Check for SSH URLs or private indicators.
	output := string(out)
	return strings.Contains(output, "git@") || strings.Contains(output, "private")
}
