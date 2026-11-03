package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
	"github.com/spf13/cobra"
)

func newHerdrCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "herdr",
		Short: "Add the container to herdr as a machine",
		Long: `Add the project's container to herdr (https://herdr.dev) as a machine.
herdr-core reaches it through 'silo connect' instead of SSH and installs its
own hc there. Agents started on that machine run inside the container.
'silo rm' removes the machine again.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			hc, err := exec.LookPath("hc")
			if err != nil {
				return fmt.Errorf("hc not found on PATH (install herdr first)")
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			server, err := incus.Connect()
			if err != nil {
				return err
			}
			if !incus.Exists(server, cfg.ContainerName) {
				return fmt.Errorf("container %s does not exist (run 'silo up' first)", cfg.ContainerName)
			}

			// herdr-core runs the connection command with its own PATH, so name
			// this binary by its absolute path.
			self, err := os.Executable()
			if err != nil {
				return fmt.Errorf("finding silo binary: %w", err)
			}
			if self, err = filepath.EvalSymlinks(self); err != nil {
				return fmt.Errorf("finding silo binary: %w", err)
			}
			via := shellQuote([]string{self, "connect", cfg.ProjectDir})

			color.Status("Adding %s to herdr...", cfg.ContainerName)
			add := exec.Command(hc, "machine", "add", "--name", cfg.ContainerName, "--install", "--via", via)
			add.Stdin, add.Stdout, add.Stderr = os.Stdin, os.Stdout, os.Stderr
			return add.Run()
		},
	}
}

// removeHerdrMachine forgets the container's herdr machine, if herdr is
// installed and knows it. Errors are only logged: most projects never use it.
func removeHerdrMachine(name string) {
	hc, err := exec.LookPath("hc")
	if err != nil {
		return
	}
	if out, err := exec.Command(hc, "machine", "rm", name).CombinedOutput(); err != nil {
		color.Debug("hc machine rm %s: %v: %s", name, err, out)
	}
}
