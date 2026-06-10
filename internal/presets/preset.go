// Package presets provides built-in, parameterized setup recipes that projects
// opt into via the `use:` config key. A preset turns a small declarative block
// (e.g. ruby versions) into the setup-phase shell commands that would otherwise
// be copy-pasted into every project's .silo.yml. Presets contribute setup
// commands only; projects keep declaring env/ports/daemons themselves.
package presets

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gschlager/silo/internal/config"
)

// Preset generates setup-phase commands from its raw YAML parameters.
type Preset interface {
	// SetupCommands returns the shell commands to run during project setup. shell
	// is the container's login shell, available for shell-specific output.
	SetupCommands(params yaml.Node, shell string) ([]string, error)
}

// registry holds the built-in presets, keyed by their `use:` name.
var registry = map[string]Preset{
	"ruby": rubyPreset{},
	"node": nodePreset{},
}

// Available returns the sorted names of all built-in presets.
func Available() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Expand turns a project's `use:` list into setup-phase commands, in declaration
// order. The result is meant to be prepended to the project's own `setup:` so
// runtimes/services are ready before project commands like `bundle install`.
func Expand(use config.UseList, shell string) ([]string, error) {
	var cmds []string
	for _, u := range use {
		p, ok := registry[u.Name]
		if !ok {
			return nil, fmt.Errorf("unknown preset %q (available: %s)", u.Name, strings.Join(Available(), ", "))
		}
		c, err := p.SetupCommands(u.Params, shell)
		if err != nil {
			return nil, fmt.Errorf("preset %q: %w", u.Name, err)
		}
		cmds = append(cmds, c...)
	}
	return cmds, nil
}

// scalarValues returns the string values of a scalar or sequence node. Values
// are read raw (node.Value) so YAML numbers like 4.0 keep their exact text
// rather than being coerced to a float and losing the trailing zero.
func scalarValues(n yaml.Node) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == "" || n.Tag == "!!null" {
			return nil
		}
		return []string{n.Value}
	case yaml.SequenceNode:
		out := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
		return out
	default:
		return nil
	}
}

// activationGuard returns a command that appends line to the shell-neutral
// activation file (~/.silo/env.sh) unless a line containing marker is already
// present, keeping re-provisioning idempotent.
func activationGuard(line, marker string) string {
	return fmt.Sprintf(`grep -qF %s "$HOME/.silo/env.sh" 2>/dev/null || printf '%%s\n' %s >> "$HOME/.silo/env.sh"`,
		shellQuote(marker), shellQuote(line))
}

// shellQuote wraps s in single quotes, escaping embedded single quotes, so it is
// safe to embed in a generated shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
