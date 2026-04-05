package cli

import (
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "silo",
		Short: "Secure isolated local environments for AI agents",
		Long: "\033[1;32msilo\033[0m " + Version + ` — Secure isolated local environments for AI agents.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := config.EnsureGlobalConfig(); err != nil {
				color.Warn("could not create global config: %v", err)
			}
			return nil
		},
	}

	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Show command output during provisioning")

	rootCmd.AddGroup(
		&cobra.Group{ID: "environment", Title: "\033[1;33mEnvironment:\033[0m"},
		&cobra.Group{ID: "agent", Title: "\033[1;33mAgents:\033[0m"},
		&cobra.Group{ID: "workflow", Title: "\033[1;33mDevelopment Workflow:\033[0m"},
		&cobra.Group{ID: "daemon", Title: "\033[1;33mDaemons:\033[0m"},
		&cobra.Group{ID: "data", Title: "\033[1;33mData Management:\033[0m"},
		&cobra.Group{ID: "config", Title: "\033[1;33mConfiguration:\033[0m"},
	)

	addCmd := func(group string, cmd *cobra.Command) {
		cmd.GroupID = group
		rootCmd.AddCommand(cmd)
	}

	// Environment lifecycle.
	addCmd("environment", newListCmd())
	addCmd("environment", newUpCmd())
	addCmd("environment", newDownCmd())
	addCmd("environment", newRmCmd())
	addCmd("environment", newEnterCmd())
	addCmd("environment", newRunCmd())
	addCmd("environment", newCpCmd())
	addCmd("environment", newStatusCmd())

	// Agents.
	addCmd("agent", newRaCmd())
	addCmd("agent", newModeCmd())

	// Development workflow.
	addCmd("workflow", newSyncCmd())
	addCmd("workflow", newPullCmd())
	addCmd("workflow", newResetCmd())
	addCmd("workflow", newUpdateCmd())

	// Daemons.
	addCmd("daemon", newStartCmd())
	addCmd("daemon", newStopCmd())
	addCmd("daemon", newRestartCmd())
	addCmd("daemon", newLogsCmd())

	// Data management.
	addCmd("data", newSnapshotCmd())

	// Configuration.
	addCmd("config", newInitCmd())
	addCmd("config", newConfigCmd())
	addCmd("config", newCompletionCmd())
	addCmd("config", newVersionCmd())

	styleHelp(rootCmd)
	registerCompletions(rootCmd)

	return rootCmd
}

func styleHelp(rootCmd *cobra.Command) {
	h := "\033[1;33m" // bold yellow for headings
	r := "\033[0m"

	usageTemplate := h + "Usage:" + r + `{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

` + h + "Aliases:" + r + `
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

` + h + "Examples:" + r + `
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

` + h + "Available Commands:" + r + `{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

` + h + "Additional Commands:" + r + `{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

` + h + "Flags:" + r + `
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

` + h + "Global Flags:" + r + `
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

` + h + "Additional help topics:" + r + `{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
	rootCmd.SetUsageTemplate(usageTemplate)
}
