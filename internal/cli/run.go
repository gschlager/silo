package cli

import (
	"os"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <command> [args...]",
		Short: "Run a single command inside the container",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			server, err := incus.Connect()
			if err != nil {
				return err
			}

			if err := requireRunning(server, cfg.ContainerName); err != nil {
				return err
			}

			opts := incus.ExecOpts{
				User:    1000,
				WorkDir: "/workspace",
			}

			// Run as: su - dev -c "cd /workspace && <command>"
			shellCmd := args[0]
			for _, a := range args[1:] {
				shellCmd += " " + a
			}

			return incus.ExecStreaming(server, cfg.ContainerName, opts, []string{
				"su", "-", cfg.User, "-c", "cd /workspace && " + shellCmd,
			}, os.Stdout, os.Stderr)
		},
	}
}
