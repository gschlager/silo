package cli

import (
	"fmt"
	"os/exec"
	"strings"

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

			var incusArgs []string
			if srcIsContainer {
				// Container → host: incus file pull
				incusArgs = append(incusArgs, "file", "pull")
				if recursive {
					incusArgs = append(incusArgs, "-r")
				}
				incusArgs = append(incusArgs, name+src, dst) // src already starts with /
			} else {
				// Host → container: incus file push
				incusArgs = append(incusArgs, "file", "push")
				if recursive {
					incusArgs = append(incusArgs, "-r")
				}
				incusArgs = append(incusArgs, src, name+dst) // dst already starts with /
			}

			c := exec.Command("incus", incusArgs...)
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			return c.Run()
		},
	}
}
