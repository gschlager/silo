package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"

	"github.com/alecthomas/chroma/v2/quick"
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
		Short: "Print the resolved global config (defaults + overrides)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadGlobalConfig()
			if err != nil {
				return err
			}
			data, err := config.MarshalYAML(cfg)
			if err != nil {
				return err
			}
			return highlightYAML(string(data))
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

func highlightYAML(src string) error {
	var buf bytes.Buffer
	err := quick.Highlight(&buf, src, "yaml", "terminal256", "monokai")
	if err != nil {
		// Fallback to plain output.
		fmt.Print(src)
		return nil
	}
	fmt.Print(buf.String())
	return nil
}
