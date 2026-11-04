package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/gschlager/silo/internal/agents"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/presets"
	"github.com/gschlager/silo/internal/provision"
	incuscli "github.com/lxc/incus/v7/client"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Config keys that mark a tmp container and record where it came from, so
// `silo tmp enter` can rebuild its config.
const (
	tmpKey      = "user.silo.tmp"
	tmpDirKey   = "user.silo.tmp.dir"
	tmpCloneKey = "user.silo.tmp.clone"
	tmpNameKey  = "user.silo.tmp.name"
)

// tmpSecretsKey is the secrets.yml entry tmp containers of cloned repos fall
// back to, e.g. for a GitHub token that can push to forks.
const tmpSecretsKey = "tmp"

func newTmpCmd() *cobra.Command {
	var agentName string
	var shell, keep, fresh bool

	cmd := &cobra.Command{
		Use:   "tmp [dir | owner/repo | url] [-- agent args...]",
		Short: "Start a throwaway container and run an agent in it",
		Long: `Start a throwaway container from the base template and run an agent in it.
When the agent exits, the container is removed.

  silo tmp                       the current directory (mounted, no .silo.yml needed)
  silo tmp ../other              another directory
  silo tmp owner/repo            a GitHub repository, cloned inside the container
  silo tmp <github issue/PR url> the repository, and the agent starts on that issue
  silo tmp <git url>             any other git repository

A directory's .silo.yml is used if it has one, except for ports (they'd clash
with the project's own container). A cloned repository only gets the global
config; its credentials come from the repo's entry in secrets.yml, or the
"tmp" entry.

A clone with uncommitted or unpushed changes isn't removed without asking.
Kept containers show up in 'silo tmp ls'.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			targetArgs, agentArgs := args, []string(nil)
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				targetArgs, agentArgs = args[:dash], args[dash:]
			}
			if len(targetArgs) > 1 {
				return fmt.Errorf("expected one directory or repository, got %d (pass agent arguments after --)", len(targetArgs))
			}
			arg := ""
			if len(targetArgs) == 1 {
				arg = targetArgs[0]
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			target, err := parseTmpTarget(arg, cwd)
			if err != nil {
				return err
			}

			name := tmpContainerName(target.Name)
			cfg, err := tmpConfig(target, name)
			if err != nil {
				return err
			}
			if agentName == "" {
				agentName = cfg.ResolveDefaultAgent()
			}
			if !shell {
				if _, ok := cfg.Agents[agentName]; !ok {
					return fmt.Errorf("unknown agent %q (configured agents: %s)", agentName, agentNames(cfg))
				}
			}
			if target.Prompt != "" && len(agentArgs) == 0 {
				agentArgs = []string{target.Prompt}
			}

			server, err := incus.Connect()
			if err != nil {
				return err
			}
			if err := provision.Provision(ctx, server, cfg, provision.Options{
				FreshBase: fresh,
				Clone:     target.Clone,
			}); err != nil {
				return err
			}
			if err := incus.UpdateConfig(ctx, server, name, map[string]string{
				tmpKey:      "true",
				tmpDirKey:   target.Dir,
				tmpCloneKey: target.Clone,
				tmpNameKey:  target.Name,
			}); err != nil {
				color.Warn("could not mark %s as a tmp container: %v", name, err)
			}

			sessionErr := runTmpSession(ctx, server, cfg, agentName, shell, agentArgs)
			finishTmp(ctx, server, cfg, target, keep)
			return sessionErr
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "Agent to run (default: from global config)")
	cmd.Flags().BoolVar(&shell, "shell", false, "Open a shell instead of running an agent")
	cmd.Flags().BoolVar(&keep, "keep", false, "Keep the container after the session")
	cmd.Flags().BoolVar(&fresh, "fresh", false, "Rebuild the base template first, for the latest packages and agents")

	cmd.AddCommand(newTmpLsCmd(), newTmpEnterCmd(), newTmpRmCmd())
	return cmd
}

// tmpTarget is what a tmp container is for: a host directory to mount or a
// repository to clone.
type tmpTarget struct {
	Dir    string // host directory to mount
	Clone  string // git URL to clone inside the container
	Name   string // short name for the container and the workspace dir
	Prompt string // initial agent prompt (for an issue or PR URL)
}

var (
	githubURLRe = regexp.MustCompile(`^(?:https?://)?(?:www\.)?github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?(?:/(issues|pull)/(\d+))?(?:[/?#].*)?$`)
	ownerRepoRe = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_.-]+$`)
)

func parseTmpTarget(arg, cwd string) (tmpTarget, error) {
	if arg == "" {
		return tmpTarget{Dir: cwd, Name: filepath.Base(cwd)}, nil
	}
	if fi, err := os.Stat(arg); err == nil && fi.IsDir() {
		dir, err := filepath.Abs(arg)
		if err != nil {
			return tmpTarget{}, err
		}
		return tmpTarget{Dir: dir, Name: filepath.Base(dir)}, nil
	}
	if m := githubURLRe.FindStringSubmatch(arg); m != nil {
		return githubTarget(m[1], m[2], m[3], m[4]), nil
	}
	if ownerRepoRe.MatchString(arg) {
		owner, repo, _ := strings.Cut(arg, "/")
		return githubTarget(owner, strings.TrimSuffix(repo, ".git"), "", ""), nil
	}
	if strings.Contains(arg, "://") || strings.HasPrefix(arg, "git@") {
		last := arg[strings.LastIndexAny(arg, "/:")+1:]
		name := strings.TrimSuffix(last, ".git")
		if name == "" {
			return tmpTarget{}, fmt.Errorf("can't tell the repository name from %q", arg)
		}
		return tmpTarget{Clone: arg, Name: name}, nil
	}
	return tmpTarget{}, fmt.Errorf("%q is neither a directory nor a repository (owner/repo or a URL)", arg)
}

func githubTarget(owner, repo, kind, number string) tmpTarget {
	t := tmpTarget{
		Clone: "https://github.com/" + owner + "/" + repo + ".git",
		Name:  repo,
	}
	if kind != "" {
		url := "https://github.com/" + owner + "/" + repo + "/" + kind + "/" + number
		t.Prompt = "Look at " + url + " and help me work on it. The repository is checked out in the current directory."
	}
	return t
}

// tmpContainerName returns a fresh name like silo-tmp-linkify-it-1a2b.
func tmpContainerName(name string) string {
	base := strings.TrimPrefix(config.ContainerName("/"+name), "silo-")
	if len(base) > 40 {
		base = strings.TrimRight(base[:40], "-")
	}
	return "silo-tmp-" + base + "-" + randomSuffix()[:4]
}

// tmpConfig builds the config for a tmp container. Unlike loadConfig, it
// doesn't register the directory as a project.
func tmpConfig(t tmpTarget, containerName string) (*config.MergedConfig, error) {
	global, err := config.LoadGlobalConfig()
	if err != nil {
		return nil, err
	}

	dir := t.Dir
	var project *config.ProjectConfig
	if dir != "" {
		project, err = config.LoadProjectConfig(dir)
		if err != nil {
			return nil, err
		}
		var use config.UseList
		if project != nil {
			use = project.Use
		}
		if err := config.ValidateEnv(global, project, use); err != nil {
			return nil, err
		}
	} else {
		// No host directory. Only the base name matters: it becomes the
		// workspace path, /workspace/<name>.
		dir = "/silo-tmp/" + t.Name
	}

	cfg := config.Merge(global, project, dir)
	cfg.ContainerName = containerName
	cfg.Ports = nil   // would clash with the project's own container
	cfg.Herdr = false // herdr reconnects by project dir, which tmp containers don't own
	if t.Dir == "" {
		cfg.ProjectKey = t.Name
		cfg.PathProjectKey = tmpSecretsKey
	}
	if shell := config.LoadContainerShell(containerName); shell != "" {
		cfg.Shell = shell
	}

	presetCmds, err := presets.Expand(cfg.Use, cfg.Shell, cfg.Presets)
	if err != nil {
		return nil, err
	}
	cfg.Setup = append(presetCmds, cfg.Setup...)
	return cfg, nil
}

func runTmpSession(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig, agentName string, shell bool, agentArgs []string) error {
	writeAgentFunctions(ctx, server, cfg)
	defer startNotifyBridge(cfg)()
	if !shell {
		return execAgent(ctx, server, cfg, agentName, agentArgs)
	}
	opts := incus.UserOpts(cfg.UserHome(), cfg.WorkspacePath())
	env, err := sessionEnv(cfg)
	if err != nil {
		return err
	}
	opts.Env = env
	return incus.ExecInteractive(ctx, server, cfg.ContainerName, opts, []string{cfg.ShellPath(), "-l"})
}

// finishTmp removes the container after a session, unless asked to keep it or
// a clone has work that only exists in the container.
func finishTmp(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig, t tmpTarget, keep bool) {
	name := cfg.ContainerName
	if !keep && t.Clone != "" {
		if unsaved := unsavedWork(ctx, server, cfg); unsaved != "" {
			fmt.Fprintf(os.Stderr, "\n%s has work that isn't pushed anywhere:\n%s\n", name, unsaved)
			keep = !confirm("Remove it anyway?")
		}
	}
	if keep {
		short := strings.TrimPrefix(name, "silo-")
		color.Info("Kept %s. Continue with `silo tmp enter %s`, remove with `silo tmp rm %s`.", name, short, short)
		return
	}
	if err := removeTmp(ctx, server, name); err != nil {
		color.Warn("could not remove %s: %v", name, err)
	}
}

// unsavedWork returns uncommitted changes and commits not on any remote in the
// cloned workspace, or "" if there are none. If it can't tell, it says so,
// which keeps the container.
func unsavedWork(ctx context.Context, server incuscli.InstanceServer, cfg *config.MergedConfig) string {
	opts := incus.UserOpts(cfg.UserHome(), cfg.WorkspacePath())
	out, err := incus.Exec(ctx, server, cfg.ContainerName, opts, cfg.LoginCmd(
		"cd "+cfg.WorkspacePath()+" && git status --short && git log --branches --not --remotes --oneline"))
	if err != nil {
		return "  (couldn't check the repository: " + err.Error() + ")"
	}
	out = strings.TrimRight(out, "\n")
	if strings.TrimSpace(out) == "" {
		return ""
	}
	lines := strings.Split(out, "\n")
	if len(lines) > 10 {
		lines = append(lines[:10], fmt.Sprintf("... and %d more", len(lines)-10))
	}
	return "  " + strings.Join(lines, "\n  ")
}

// confirm asks a yes/no question, defaulting to no. Without a terminal it
// answers no.
func confirm(question string) bool {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y")
}

func removeTmp(ctx context.Context, server incuscli.InstanceServer, name string) error {
	color.Status("Removing %s...", name)
	if incus.IsRunning(server, name) {
		if err := incus.Stop(ctx, server, name); err != nil {
			return err
		}
	}
	if err := incus.Delete(ctx, server, name); err != nil {
		return err
	}
	return agents.CleanupContainerDirs(name, true)
}

// tmpInstance looks up a tmp container by name, with or without "silo-".
func tmpInstance(server incuscli.InstanceServer, name string) (string, map[string]string, error) {
	if !strings.HasPrefix(name, "silo-") {
		name = "silo-" + name
	}
	inst, err := incus.GetInstance(server, name)
	if err != nil {
		return "", nil, err
	}
	if inst.Config[tmpKey] != "true" {
		return "", nil, fmt.Errorf("%s is not a tmp container", name)
	}
	return name, inst.Config, nil
}

func newTmpEnterCmd() *cobra.Command {
	var agentName string
	var shell bool
	cmd := &cobra.Command{
		Use:   "enter <name> [-- agent args...]",
		Short: "Run an agent (or a shell) in a kept tmp container",
		Args:  requireArgs(1, "container name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			server, err := incus.Connect()
			if err != nil {
				return err
			}
			name, conf, err := tmpInstance(server, args[0])
			if err != nil {
				return err
			}
			t := tmpTarget{Dir: conf[tmpDirKey], Clone: conf[tmpCloneKey], Name: conf[tmpNameKey]}
			cfg, err := tmpConfig(t, name)
			if err != nil {
				return err
			}
			if agentName == "" {
				agentName = cfg.ResolveDefaultAgent()
			}
			if err := ensureRunning(ctx, server, name); err != nil {
				return err
			}
			return runTmpSession(ctx, server, cfg, agentName, shell, args[1:])
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "Agent to run (default: from global config)")
	cmd.Flags().BoolVar(&shell, "shell", false, "Open a shell instead of running an agent")
	return cmd
}

func newTmpRmCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "rm [name...]",
		Short: "Remove kept tmp containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !all {
				return fmt.Errorf("name a container or pass --all")
			}
			server, err := incus.Connect()
			if err != nil {
				return err
			}
			names := args
			if all {
				names = nil
				instances, err := incus.ListSiloInstances(server)
				if err != nil {
					return err
				}
				for _, inst := range instances {
					if inst.Config[tmpKey] == "true" {
						names = append(names, inst.Name)
					}
				}
			}
			for _, n := range names {
				name, _, err := tmpInstance(server, n)
				if err != nil {
					return err
				}
				if err := removeTmp(cmd.Context(), server, name); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Remove all tmp containers")
	return cmd
}

func newTmpLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List kept tmp containers",
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
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATUS\tSOURCE\tCREATED")
			n := 0
			for _, inst := range instances {
				if inst.Config[tmpKey] != "true" {
					continue
				}
				source := inst.Config[tmpDirKey]
				if source == "" {
					source = inst.Config[tmpCloneKey]
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", strings.TrimPrefix(inst.Name, "silo-"), inst.Status, source,
					inst.CreatedAt.Local().Format("2006-01-02 15:04"))
				n++
			}
			if n == 0 {
				fmt.Println("No tmp containers.")
				return nil
			}
			return w.Flush()
		},
	}
}
