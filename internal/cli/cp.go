package cli

import (
	"fmt"
	"strings"

	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newCpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cp <src> <dst>",
		Short: "Copy files between host and container",
		Long: `Copy files or directories between the host and the container.
Prefix container paths with : to distinguish them from host paths.

Examples:
  silo cp ./local-file.txt :/workspace/file.txt     # host → container
  silo cp -r ./local-dir :/workspace/dir             # host → container (recursive)
  silo cp :/home/dev/output.log ./output.log          # container → host
  silo cp -r :/workspace/results ./results            # container → host (recursive)`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, a := range args {
				if a == "--help" || a == "-h" {
					return cmd.Help()
				}
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			server, err := incus.Connect()
			if err != nil {
				return err
			}

			// Parse flags and args manually.
			recursive := false
			var paths []string
			for _, a := range args {
				if a == "-r" || a == "--recursive" {
					recursive = true
				} else {
					paths = append(paths, a)
				}
			}

			if len(paths) != 2 {
				return fmt.Errorf("expected 2 arguments: source and destination\n\n%s", cmd.UsageString())
			}

			src, dst := paths[0], paths[1]
			srcIsContainer := strings.HasPrefix(src, ":")
			dstIsContainer := strings.HasPrefix(dst, ":")

			if srcIsContainer == dstIsContainer {
				return fmt.Errorf("exactly one path must start with : (container side)")
			}

			name := cfg.ContainerName

			if srcIsContainer {
				return incus.PullFile(server, name, src[1:], dst, recursive)
			}
			return incus.PushFile(server, name, src, dst[1:], recursive)
		},
	}
}
