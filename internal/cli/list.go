package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"os"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all silo containers",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			server, err := incus.Connect()
			if err != nil {
				return err
			}

			instances, err := incus.ListSiloInstances(server)
			if err != nil {
				return err
			}

			if len(instances) == 0 {
				fmt.Println("No silo containers.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATUS\tIMAGE\tCREATED")
			for _, inst := range instances {
				image := inst.Config["image.description"]
				if image == "" {
					image = inst.Config["image.os"]
				}
				// Trim "silo-" prefix for display.
				displayName := strings.TrimPrefix(inst.Name, "silo-")
				created := inst.CreatedAt.Format("2006-01-02 15:04")
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", displayName, inst.Status, image, created)
			}
			w.Flush()
			return nil
		},
	}
}
