package cli

import (
	"fmt"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs [daemon]",
		Short: "Tail logs for a specific daemon or all daemons",
		Long: `Without arguments, tails all daemon logs interleaved.
With a daemon name, tails logs for that specific daemon.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

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

			var journalCmd string
			if len(args) == 0 {
				journalCmd = "journalctl --user -u 'silo-*' -f"
			} else {
				daemon := args[0]
				if _, ok := cfg.Daemons[daemon]; !ok {
					return fmt.Errorf("unknown daemon %q", daemon)
				}
				journalCmd = fmt.Sprintf("journalctl --user -u silo-%s -f", daemon)
			}

			opts := incus.UserOpts(cfg.UserHome(), "")
			opts.Env = cfg.HostEnv()
			return incus.ExecInteractive(ctx, server, cfg.ContainerName, opts,
				cfg.LoginCmd(journalCmd))
		},
	}
}
