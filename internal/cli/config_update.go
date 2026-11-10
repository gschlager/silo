package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/configupdate"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newConfigUpdateCmd() *cobra.Command {
	var yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update this project's .silo.yml to the current silo",
		Long: `Find things in .silo.yml and .silo.local.yml that are out of date with
this silo version, show each change as a diff and apply it when you agree:

  - a secrets.yml entry named <project>-<hash> (from a short-lived silo
    version), which is renamed to the project's name
  - an older release of the default image (fedora/44 while it's fedora/45)
  - mounts:, which project files can't declare anymore; they move to
    project_mounts in the global config
  - git.credential and tools.<name>.credential, which move to secrets.yml
  - port forwards on a usual web server port (3000, 4200, 8080, ...)
    without http: true, which gives them a <project>.localhost name
  - keys silo doesn't know (anymore), e.g. docker: true becomes nesting: true
    and a typo like deamons: becomes daemons:

Comments and formatting are kept. silo up mentions it when there's
something to update.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			return runConfigUpdate(cwd, yes, dryRun)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Apply every suggestion without asking (except removing unknown keys)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Only show the suggestions")
	return cmd
}

func runConfigUpdate(dir string, yes, dryRun bool) error {
	key := projectSecretsKey(dir)
	declined := map[string]bool{}
	applied := 0
	for {
		all, err := configupdate.Check(dir, key)
		if err != nil {
			return err
		}
		var pending []configupdate.Suggestion
		for _, s := range all {
			if !declined[s.ID] {
				pending = append(pending, s)
			}
		}
		if len(pending) == 0 {
			break
		}
		if applied == 0 && len(declined) == 0 {
			color.Info("%d suggestions for this project's config.", len(pending))
		}

		// One at a time: after applying one, the others are checked again
		// against the changed file.
		s := pending[0]
		printSuggestion(dir, s)
		if dryRun {
			declined[s.ID] = true
			continue
		}
		ok := false
		switch {
		case yes && !s.Careful:
			ok = true
		case !term.IsTerminal(int(os.Stdin.Fd())):
			ok = false
		default:
			ok = ask("Apply this change?", !s.Careful)
		}
		if !ok {
			declined[s.ID] = true
			continue
		}
		if err := s.Apply(); err != nil {
			color.Warn("couldn't apply it: %v", err)
			declined[s.ID] = true
			continue
		}
		applied++
		color.Success("Done.")
	}

	switch {
	case dryRun && len(declined) > 0:
		color.Info("Run silo config update to apply them.")
	case applied == 0 && len(declined) == 0:
		color.Success("This project's config is up to date.")
	case applied > 0:
		color.Success("Applied %d changes.", applied)
		if len(declined) > 0 {
			color.Info("Skipped %d.", len(declined))
		}
	}
	return nil
}

// projectSecretsKey is the project's name in secrets.yml, or "" when the
// directory isn't a silo project yet (then credentials aren't moved).
func projectSecretsKey(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, ".silo.yml")); err != nil {
		if _, err := os.Stat(filepath.Join(dir, ".silo.yaml")); err != nil {
			return ""
		}
	}
	name, err := config.AssignContainerName(dir)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(name, "silo-")
}

var (
	diffDel  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	diffAdd  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	diffDim  = lipgloss.NewStyle().Faint(true)
	diffHead = lipgloss.NewStyle().Bold(true)
)

func printSuggestion(dir string, s configupdate.Suggestion) {
	fmt.Println()
	fmt.Println(diffHead.Render("• " + s.Title))
	fmt.Println("  " + s.Detail)
	printDiff(relPath(dir, s.File), s.Before, s.After)
	for _, o := range s.Others {
		printDiff(o.Path, o.Before, o.After)
	}
	fmt.Println()
}

func printDiff(name, before, after string) {
	fmt.Println(diffDim.Render("  " + name))
	for _, l := range configupdate.Diff(before, after, 2) {
		switch l.Op {
		case 0:
			fmt.Println(diffDim.Render("    …"))
		case '-':
			fmt.Println(diffDel.Render("  - " + l.Text))
		case '+':
			fmt.Println(diffAdd.Render("  + " + l.Text))
		default:
			fmt.Println(diffDim.Render("    " + l.Text))
		}
	}
}

func relPath(dir, path string) string {
	if r, err := filepath.Rel(dir, path); err == nil {
		return r
	}
	return path
}

// ask asks a yes/no question with a default.
func ask(question string, def bool) bool {
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	fmt.Fprintf(os.Stderr, "%s %s ", question, hint)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "" {
		return def
	}
	return strings.HasPrefix(answer, "y")
}
