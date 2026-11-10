package configupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gschlager/silo/internal/config"
	"gopkg.in/yaml.v3"
)

// readOptional reads a file, returning "" when it doesn't exist.
func readOptional(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

// writeAtomic writes a file through a temporary file in the same directory.
func writeAtomic(path, content string, mode os.FileMode) error {
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// childIndent is the indentation of the first entry of a block mapping or
// sequence, or parentIndent+2 when it has none.
func (d *doc) childIndent(val *yaml.Node, parentIndent int) int {
	if val != nil && len(val.Content) > 0 {
		// The line's indentation, not the node's column: a sequence item's
		// value starts after "- ".
		return indentOf(d.lines[val.Content[0].Line-1])
	}
	return parentIndent + 2
}

func (d *doc) insert(at int, lines ...string) {
	d.lines = slices.Insert(d.lines, at, lines...)
}

// withSecret returns the secrets file content with name: value added to the
// project's entry. It refuses to change a different value that's there.
func withSecret(src, project, name, value string) (string, error) {
	if strings.TrimSpace(src) == "" {
		src = "# silo per-project secrets — injected as environment variables.\n" +
			"# Each value is a 1Password reference (op://vault/item/field) or a literal.\n" +
			"# The reserved 'github' key wires the git credential helper and GH_TOKEN.\n"
	}
	d, err := parseDoc(src)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", config.SecretsPath(), err)
	}
	line := fmt.Sprintf("%s: %s", name, quote(value))
	e, ok := d.get(project)
	switch {
	case !ok:
		text := strings.TrimRight(d.text(), "\n")
		return text + "\n\n" + project + ":\n  " + line + "\n", nil
	case e.val.Kind == yaml.ScalarNode && e.val.Tag == "!!null":
		// A stub ("project:" with only comments under it).
		d.insert(e.key.Line, "  "+line)
	case e.val.Kind == yaml.MappingNode && e.val.Style&yaml.FlowStyle == 0:
		if cur, ok := d.get(project, name); ok {
			if cur.val.Value == value {
				return d.text(), nil
			}
			return "", fmt.Errorf("%s already sets %s for %s to something else; change it there", config.SecretsPath(), name, project)
		}
		_, end := d.span(e)
		d.insert(end, strings.Repeat(" ", d.childIndent(e.val, e.key.Column-1))+line)
	default:
		return "", fmt.Errorf("can't add to the %s entry in %s; add %s yourself", project, config.SecretsPath(), line)
	}
	return d.text(), nil
}

// withProjectMounts returns the global config with mounts added under
// project_mounts.<key>.
func withProjectMounts(src, key string, mounts []string) (string, error) {
	d, err := parseDoc(src)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", config.GlobalConfigPath(), err)
	}
	items := func(indent int, have []string) []string {
		var out []string
		for _, m := range mounts {
			if !slices.Contains(have, m) {
				out = append(out, strings.Repeat(" ", indent)+"- "+quote(m))
			}
		}
		return out
	}
	pm, ok := d.get("project_mounts")
	if !ok || d.root == nil {
		block := append([]string{"project_mounts:", "  " + key + ":"}, items(4, nil)...)
		text := strings.TrimRight(d.text(), "\n")
		if text != "" {
			text += "\n\n"
		}
		return text + strings.Join(block, "\n") + "\n", nil
	}
	switch {
	case pm.val.Kind == yaml.ScalarNode && pm.val.Tag == "!!null":
		d.insert(pm.key.Line, append([]string{"  " + key + ":"}, items(4, nil)...)...)
	case pm.val.Kind == yaml.MappingNode && pm.val.Style&yaml.FlowStyle == 0:
		indent := d.childIndent(pm.val, 0)
		if e, ok := d.get("project_mounts", key); ok {
			if e.val.Kind != yaml.SequenceNode || e.val.Style&yaml.FlowStyle != 0 {
				return "", fmt.Errorf("can't add to project_mounts.%s in %s; add the mounts yourself", key, config.GlobalConfigPath())
			}
			var have []string
			for _, n := range e.val.Content {
				have = append(have, n.Value)
			}
			_, end := d.span(e)
			d.insert(end, items(d.childIndent(e.val, indent), have)...)
		} else {
			_, end := d.span(pm)
			d.insert(end, append([]string{strings.Repeat(" ", indent) + key + ":"}, items(indent+2, nil)...)...)
		}
	default:
		return "", fmt.Errorf("can't add to project_mounts in %s; add the mounts yourself", config.GlobalConfigPath())
	}
	return d.text(), nil
}
