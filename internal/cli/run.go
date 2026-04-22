package cli

import (
	"os"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "run <command> [args...]",
		Short:              "Run a single command inside the container",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Handle --help manually since flag parsing is disabled.
			for _, a := range args {
				if a == "--help" || a == "-h" {
					return cmd.Help()
				}
			}
			if len(args) == 0 {
				return cmd.Help()
			}
			ctx := cmd.Context()

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			server, err := incus.Connect()
			if err != nil {
				return err
			}

			if err := ensureRunning(ctx, server, cfg.ContainerName); err != nil {
				return err
			}

			opts := incus.UserOpts(cfg.UserHome(), cfg.WorkspacePath())

			shellCmd := shellQuote(args)
			loginCmd := cfg.LoginCmd("cd " + cfg.WorkspacePath() + " && " + shellCmd)

			if term.IsTerminal(int(os.Stdin.Fd())) {
				opts.Env = sessionEnv(cfg)
				return incus.ExecInteractive(ctx, server, cfg.ContainerName, opts, loginCmd)
			}

			return incus.ExecStreaming(ctx, server, cfg.ContainerName, opts,
				loginCmd,
				os.Stdout, os.Stderr)
		},
	}
}
