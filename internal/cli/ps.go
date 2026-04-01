package cli

import (
	"fmt"
	"os"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newPsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ps",
		Short: "Show container status and running daemons",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			server, err := incus.Connect()
			if err != nil {
				return err
			}

			name := cfg.ContainerName

			if !incus.Exists(server, name) {
				return fmt.Errorf("container %s does not exist", name)
			}

			inst, err := incus.GetInstance(server, name)
			if err != nil {
				return err
			}

			fmt.Printf("Container: %s\n", name)
			fmt.Printf("Status:    %s\n", inst.Status)
			fmt.Printf("Image:     %s\n", cfg.Image)

			if inst.Status != "Running" {
				return nil
			}

			// Show daemons.
			if len(cfg.Daemons) > 0 {
				fmt.Println("\nDaemons:")
				output, err := incus.Exec(server, name, incus.ExecOpts{}, []string{
					"su", "-", cfg.User, "-c",
					"systemctl --user list-units 'silo-*' --no-pager --no-legend 2>/dev/null || true",
				})
				if err == nil && output != "" {
					fmt.Print(output)
				} else {
					for daemon := range cfg.Daemons {
						fmt.Fprintf(os.Stderr, "  silo-%s (status unknown)\n", daemon)
					}
				}
			}

			return nil
		},
	}
}
