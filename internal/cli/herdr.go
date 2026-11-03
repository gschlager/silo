package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
)

// herdrTarget is a machine as `hc machine ls --json` lists it.
type herdrTarget struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

// ensureHerdrMachine adds the container to herdr (https://herdr.dev) as a
// machine when herdr is installed, so its agents show up in the app. herdr-core
// reaches it through `silo connect` instead of SSH and copies its own hc there.
// A newly added machine also gets a shell labeled with the project's space, so
// the space shows up in the sidebar before any agent runs. Errors are only
// warnings: herdr is optional.
func ensureHerdrMachine(cfg *config.MergedConfig) {
	hc, err := exec.LookPath("hc")
	if err != nil {
		return
	}
	if err := addHerdrMachine(hc, cfg); err != nil {
		color.Warn("could not add %s to herdr: %v", cfg.ContainerName, err)
	}
}

func addHerdrMachine(hc string, cfg *config.MergedConfig) error {
	// herdr-core runs the connection command with its own PATH, so name this
	// binary by its absolute path.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding silo binary: %w", err)
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return fmt.Errorf("finding silo binary: %w", err)
	}
	via := shellQuote([]string{self, "connect", cfg.ProjectDir})

	out, err := exec.Command(hc, "machine", "ls", "--json").Output()
	if err != nil {
		return fmt.Errorf("hc machine ls: %w", err)
	}
	var list struct {
		Machines []herdrTarget `json:"machines"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return fmt.Errorf("parsing hc machine ls: %w", err)
	}

	known := false
	for _, m := range list.Machines {
		if m.Name != cfg.ContainerName {
			continue
		}
		if m.Target == via {
			return nil
		}
		// Reached another way (the project moved, or another silo binary):
		// forget it and add it again. Its terminals keep running.
		color.Debug("herdr reaches %s with %s, replacing it", m.Name, m.Target)
		if out, err := exec.Command(hc, "machine", "rm", m.Name).CombinedOutput(); err != nil {
			return fmt.Errorf("hc machine rm: %w: %s", err, out)
		}
		known = true
	}

	color.Status("Adding %s to herdr...", cfg.ContainerName)
	if out, err := exec.Command(hc, "machine", "add", "--name", cfg.ContainerName, "--install", "--via", via).CombinedOutput(); err != nil {
		return fmt.Errorf("hc machine add: %w: %s", err, out)
	}
	if known {
		return nil
	}

	// No argv: that machine's own shell.
	if out, err := exec.Command(hc, "run", "--bg", "--on", cfg.ContainerName, "--no-parent",
		"--cwd", cfg.WorkspacePath(),
		"--label", "herdr.space="+herdrSpace(cfg),
		"--label", "herdr.tab=shell").CombinedOutput(); err != nil {
		return fmt.Errorf("opening the project's space: %w: %s", err, out)
	}
	return nil
}

// herdrSpace is the herdr space id for the project's terminals. herdr creates a
// space for an id it doesn't know yet, so every silo terminal of the project
// lands in the same one.
func herdrSpace(cfg *config.MergedConfig) string {
	return cfg.ProjectKey
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

// herdrLaunch returns a shell prefix that, when the container has herdr's hc,
// starts the agent as a terminal of the container's herdr-core and attaches to
// it, so a silo ra session shows up in herdr like one started there. The agent
// then gets the core's env (see silo connect), not this session's, and keeps
// running after ctrl+\ detaches. Without hc, or if starting it fails, the
// shell falls through to running the agent directly.
func herdrLaunch(cfg *config.MergedConfig, agentName, agentCmd string) string {
	name := herdrTerminalName("ra-" + agentName + "-" + time.Now().Format("150405"))
	run := shellQuote([]string{"hc", "run", "--bg", "--rm", "--no-parent", "--name", name,
		"--cwd", cfg.WorkspacePath(),
		"--label", "herdr.space=" + herdrSpace(cfg),
		"--label", "herdr.tab=" + name,
		"--", cfg.ShellPath(), "-lc", agentCmd})
	return "if command -v hc >/dev/null 2>&1 && " + run + " >/dev/null; then " +
		`echo 'silo: running in herdr; ctrl+\ detaches and leaves the agent running' >&2; ` +
		"exec hc attach " + name + "; fi; "
}

// herdrTerminalName makes s a valid hc terminal name: 1-32 of a-z 0-9 - _.
func herdrTerminalName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		}
		return '-'
	}, s)
	return s[:min(len(s), 32)]
}
