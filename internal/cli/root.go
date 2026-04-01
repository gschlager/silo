package cli

import (
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/update"
	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "silo",
		Short: "Secure isolated local environments for AI agents",
		Long: `silo creates isolated development environments using Incus system containers.
It provides full network and service isolation while keeping your existing
workflow (host-side IDE, git client, DB inspector) intact via bind mounts
and port forwarding.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := config.EnsureGlobalConfig(); err != nil {
				color.Warn("could not create global config: %v", err)
			}
			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			// Background update check — non-blocking.
			if Version != "dev" {
				if newVersion := update.CheckForUpdate(Version); newVersion != "" {
					color.Infof("A new version of silo is available (%s). Run 'silo upgrade' to update.", newVersion)
				}
			}
		},
	}

	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Show command output during provisioning")

	rootCmd.AddCommand(
		newVersionCmd(),
		newUpCmd(),
		newDownCmd(),
		newEnterCmd(),
		newRunCmd(),
		newRmCmd(),
		newPsCmd(),
		newStatusCmd(),
		newRaCmd(),
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newLogsCmd(),
		newSyncCmd(),
		newPullCmd(),
		newResetCmd(),
		newUpdateCmd(),
		newSnapshotCmd(),
		newRestoreCmd(),
		newInitCmd(),
		newConfigCmd(),
		newCompletionCmd(),
		newUpgradeCmd(),
	)

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
