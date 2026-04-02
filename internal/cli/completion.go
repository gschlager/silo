package cli

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/gschlager/silo/internal/color"
	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for silo.

Print completions to stdout:
  silo completion bash
  silo completion zsh
  silo completion fish

Auto-install to the standard location:
  silo completion install`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			rootCmd := cmd.Root()
			switch args[0] {
			case "bash":
				return rootCmd.GenBashCompletion(os.Stdout)
			case "zsh":
				return rootCmd.GenZshCompletion(os.Stdout)
			case "fish":
				return rootCmd.GenFishCompletion(os.Stdout, true)
			default:
				return cmd.Usage()
			}
		},
	}

	cmd.AddCommand(newCompletionInstallCmd())
	return cmd
}

func newCompletionInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install [bash|zsh|fish]",
		Short: "Install completions to the standard location",
		Long: `Auto-detect or specify the shell and install completions.

Without arguments, detects your current shell from $SHELL.
With an argument, installs for that shell.`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			shell := ""
			if len(args) > 0 {
				shell = args[0]
			} else {
				shell = detectShell()
				if shell == "" {
					return fmt.Errorf("could not detect shell from $SHELL; specify one: silo completion install [bash|zsh|fish]")
				}
			}

			rootCmd := cmd.Root()

			switch shell {
			case "zsh":
				return installZsh(rootCmd)
			case "bash":
				return installBash(rootCmd)
			case "fish":
				return installFish(rootCmd)
			default:
				return fmt.Errorf("unsupported shell %q; supported: bash, zsh, fish", shell)
			}
		},
	}
}

func detectShell() string {
	shell := filepath.Base(os.Getenv("SHELL"))
	switch shell {
	case "bash", "zsh", "fish":
		return shell
	}
	return ""
}

func installZsh(rootCmd *cobra.Command) error {
	// Try user completions dir first, fall back to site-functions.
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".zsh", "completions"),
		filepath.Join(home, ".local", "share", "zsh", "site-functions"),
		"/usr/local/share/zsh/site-functions",
	}

	dir := ""
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			dir = c
			break
		}
	}
	if dir == "" {
		// Create user completions dir.
		dir = candidates[0]
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		color.Info("Created %s — add it to your fpath in .zshrc:", dir)
		color.Info("  fpath=(%s $fpath)", dir)
	}

	path := filepath.Join(dir, "_silo")
	if err := rootCmd.GenZshCompletionFile(path); err != nil {
		return err
	}
	color.Success("Installed zsh completions to %s", path)
	return nil
}

func installBash(rootCmd *cobra.Command) error {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "share", "bash-completion", "completions"),
		"/etc/bash_completion.d",
	}

	dir := ""
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			dir = c
			break
		}
	}
	if dir == "" {
		dir = candidates[0]
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	path := filepath.Join(dir, "silo")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()
	if err := rootCmd.GenBashCompletion(f); err != nil {
		return err
	}
	color.Success("Installed bash completions to %s", path)
	return nil
}

func installFish(rootCmd *cobra.Command) error {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "fish", "completions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	path := filepath.Join(dir, "silo.fish")
	if err := rootCmd.GenFishCompletionFile(path, true); err != nil {
		return err
	}
	color.Success("Installed fish completions to %s", path)
	return nil
}

// registerCompletions sets up dynamic completions for various commands.
func registerCompletions(rootCmd *cobra.Command) {
	for _, cmd := range rootCmd.Commands() {
		switch cmd.Use {
		case "ra <agent> [prompt or file]":
			cmd.ValidArgsFunction = completeAgentNames
		case "start <daemon>", "stop <daemon>":
			cmd.ValidArgsFunction = completeDaemonNames
		case "reset <target>":
			cmd.ValidArgsFunction = completeResetTargets
		}
	}

	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "restart" || cmd.Name() == "logs" {
			cmd.ValidArgsFunction = completeDaemonNames
		}
	}
}

func completeAgentNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	cfg, err := loadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return slices.Collect(maps.Keys(cfg.Agents)), cobra.ShellCompDirectiveNoFileComp
}

func completeDaemonNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := loadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return slices.Collect(maps.Keys(cfg.Daemons)), cobra.ShellCompDirectiveNoFileComp
}

func completeResetTargets(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := loadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return slices.Collect(maps.Keys(cfg.Reset)), cobra.ShellCompDirectiveNoFileComp
}
