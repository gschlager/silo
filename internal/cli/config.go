package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/gschlager/silo/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage global configuration",
	}

	cmd.AddCommand(
		newConfigEditCmd(),
		newConfigShowCmd(),
		newConfigPathCmd(),
	)
	return cmd
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open global config in $EDITOR",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.GlobalConfigPath()

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = os.Getenv("VISUAL")
			}
			if editor == "" {
				return fmt.Errorf("$EDITOR is not set; edit %s manually", path)
			}

			editorCmd := exec.Command(editor, path)
			editorCmd.Stdin = os.Stdin
			editorCmd.Stdout = os.Stdout
			editorCmd.Stderr = os.Stderr
			return editorCmd.Run()
		},
	}
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the global config file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.GlobalConfigPath()
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}
			fmt.Print(string(data))
			return nil
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the global config file path",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(config.GlobalConfigPath())
		},
	}
}
