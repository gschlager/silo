package cli

import (
	"maps"
	"os"
	"slices"

	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	var install bool

	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for silo.

To load completions:

  bash:
    source <(silo completion bash)

  zsh:
    silo completion zsh > "${fpath[1]}/_silo"

  fish:
    silo completion fish | source`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			rootCmd := cmd.Root()
			switch args[0] {
			case "bash":
				return rootCmd.GenBashCompletion(os.Stdout)
			case "zsh":
				if install {
					return rootCmd.GenZshCompletionFile("/usr/local/share/zsh/site-functions/_silo")
				}
				return rootCmd.GenZshCompletion(os.Stdout)
			case "fish":
				return rootCmd.GenFishCompletion(os.Stdout, true)
			default:
				return cmd.Usage()
			}
		},
	}

	cmd.Flags().BoolVar(&install, "install", false, "Install completions to standard location")
	return cmd
}

// registerCompletions sets up dynamic completions for various commands.
func registerCompletions(rootCmd *cobra.Command) {
	// Find commands that need custom completions.
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

	// restart can be a daemon name.
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "restart" {
			cmd.ValidArgsFunction = completeDaemonNames
		}
		if cmd.Name() == "logs" {
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
