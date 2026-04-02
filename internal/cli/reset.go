package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset <target>",
		Short: "Run the named reset command list",
		Long:  `Run the named reset command list (e.g., silo reset db).`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			target := args[0]
			commands, ok := cfg.Reset[target]
			if !ok {
				var targets []string
				for t := range cfg.Reset {
					targets = append(targets, t)
				}
				return fmt.Errorf("unknown reset target %q (available: %s)", target, strings.Join(targets, ", "))
			}

			server, err := incus.Connect()
			if err != nil {
				return err
			}

			if err := requireRunning(server, cfg.ContainerName); err != nil {
				return err
			}

			opts := incus.UserOpts(cfg.UserHome(), "/workspace")
			for _, resetCmd := range commands {
				color.Status("%s", resetCmd)
				if err := incus.ExecStreaming(ctx, server, cfg.ContainerName, opts,
					cfg.LoginCmd("cd /workspace && "+resetCmd),
					os.Stdout, os.Stderr); err != nil {
					return fmt.Errorf("reset command %q: %w", resetCmd, err)
				}
			}
			return nil
		},
	}
}
