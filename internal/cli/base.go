package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	"github.com/spf13/cobra"
)

func newBaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "base",
		Short: "Manage the base templates new containers are copied from",
		Long: `New containers are copied from a base template instead of being built from
the bare image. A template is a stopped container per image with default_setup,
the login shell, the dev user and the globally enabled agents already set up.

silo builds a template on first use and rebuilds it when any of those inputs
change. Without a subcommand, lists the existing templates.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listBases()
		},
	}
	cmd.AddCommand(newBaseListCmd(), newBaseRebuildCmd(), newBaseRmCmd())
	return cmd
}

func newBaseListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the base templates",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listBases()
		},
	}
}

func listBases() error {
	server, err := incus.Connect()
	if err != nil {
		return err
	}
	bases, err := provision.ListBases(server)
	if err != nil {
		return err
	}
	if len(bases) == 0 {
		fmt.Println("No base templates. One is built the next time a container is created.")
		return nil
	}

	// Mark the template the current directory would use as current or outdated.
	spec, specErr := baseSpecForCwd()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tIMAGE\tAGENTS\tBUILT\t")
	for _, b := range bases {
		built := "-"
		if !b.BuiltAt.IsZero() {
			built = b.BuiltAt.Local().Format("2006-01-02 15:04")
		}
		note := ""
		if specErr == nil && b.Name == provision.BaseName(spec.Image) {
			if b.Hash == spec.Hash() {
				note = "(current)"
			} else {
				note = "(outdated, rebuilt on next use)"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", b.Name, b.Image, b.Agents, built, note)
	}
	return w.Flush()
}

func newBaseRebuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild the base template for the current project's image",
		Long: `Rebuild the base template for the current project's image (or the default
image outside a project) from scratch, e.g. to pick up new packages and agent
versions. Existing containers are not affected.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := baseSpecForCwd()
			if err != nil {
				return err
			}
			server, err := incus.Connect()
			if err != nil {
				return err
			}
			if err := provision.RebuildBase(cmd.Context(), server, spec); err != nil {
				return err
			}
			color.Success("Base template %s is ready.", provision.BaseName(spec.Image))
			return nil
		},
	}
}

func newBaseRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm [name...]",
		Short: "Remove base templates (all of them without arguments)",
		Long: `Remove base templates. Without arguments, removes all of them. Existing
containers are not affected; the next new container rebuilds its template.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			server, err := incus.Connect()
			if err != nil {
				return err
			}
			names := args
			if len(names) == 0 {
				bases, err := provision.ListBases(server)
				if err != nil {
					return err
				}
				for _, b := range bases {
					names = append(names, b.Name)
				}
			}
			for _, name := range names {
				if !strings.HasPrefix(name, "silo-") {
					name = "silo-" + name
				}
				color.Status("Removing %s...", name)
				if err := provision.RemoveBase(cmd.Context(), server, name); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// baseSpecForCwd returns the base template spec for the current directory.
// Unlike loadConfig it doesn't register the directory as a project, so it's
// safe to call anywhere.
func baseSpecForCwd() (provision.BaseSpec, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return provision.BaseSpec{}, err
	}
	global, err := config.LoadGlobalConfig()
	if err != nil {
		return provision.BaseSpec{}, err
	}
	project, err := config.LoadProjectConfig(cwd)
	if err != nil {
		return provision.BaseSpec{}, err
	}
	return provision.BaseSpecFor(config.Merge(global, project, cwd)), nil
}
