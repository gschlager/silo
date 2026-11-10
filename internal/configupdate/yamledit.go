package configupdate

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// doc is a YAML file kept as text. Changes are made to the text at the
// positions yaml.v3 reports, so comments, blank lines and formatting the
// user chose stay as they are.
type doc struct {
	lines []string // without line endings
	root  *yaml.Node
}

func parseDoc(src string) (*doc, error) {
	d := &doc{lines: strings.Split(src, "\n")}
	var file yaml.Node
	if err := yaml.Unmarshal([]byte(src), &file); err != nil {
		return nil, err
	}
	if len(file.Content) > 0 && file.Content[0].Kind == yaml.MappingNode {
		d.root = file.Content[0]
	}
	return d, nil
}

func (d *doc) text() string { return strings.Join(d.lines, "\n") }

// entry is a key/value pair of a mapping.
type entry struct {
	key, val *yaml.Node
	parent   *yaml.Node // the mapping
}

// get returns the entry for a key path like "git", "credential".
func (d *doc) get(path ...string) (entry, bool) {
	m := d.root
	var e entry
	for _, k := range path {
		if m == nil || m.Kind != yaml.MappingNode {
			return entry{}, false
		}
		found := false
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == k {
				e = entry{key: m.Content[i], val: m.Content[i+1], parent: m}
				m = m.Content[i+1]
				found = true
				break
			}
		}
		if !found {
			return entry{}, false
		}
	}
	return e, e.key != nil
}

// entries returns the entries of a mapping node in order.
func entries(m *yaml.Node) []entry {
	var out []entry
	if m == nil || m.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		out = append(out, entry{key: m.Content[i], val: m.Content[i+1], parent: m})
	}
	return out
}

// indentOf returns the number of leading spaces of a line.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func blankOrComment(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// span returns the lines [start, end) an entry occupies: its head comment,
// the key line and everything indented below it.
func (d *doc) span(e entry) (int, int) {
	start := e.key.Line - 1
	// Comment lines directly above belong to the entry.
	for start > 0 {
		t := strings.TrimSpace(d.lines[start-1])
		if !strings.HasPrefix(t, "#") || indentOf(d.lines[start-1]) != e.key.Column-1 {
			break
		}
		start--
	}
	end := e.key.Line
	keyIndent := e.key.Column - 1
	last := end
	for ; end < len(d.lines); end++ {
		line := d.lines[end]
		if blankOrComment(line) {
			continue
		}
		ind := indentOf(line)
		// Deeper lines belong to the entry, and so do sequence items at the
		// key's own indentation ("mounts:\n- a").
		if ind > keyIndent || (ind == keyIndent && strings.HasPrefix(strings.TrimSpace(line), "- ")) {
			last = end + 1
			continue
		}
		break
	}
	// Leave blank lines and comments after the entry's last line alone;
	// they belong to whatever follows.
	return start, last
}

// remove deletes an entry. When it was the only entry of its parent
// mapping, the parent entry goes too, so no empty "git:" is left behind.
func (d *doc) remove(path ...string) error {
	e, ok := d.get(path...)
	if !ok {
		return fmt.Errorf("%s not found", strings.Join(path, "."))
	}
	if len(path) > 1 && len(e.parent.Content) == 2 {
		return d.remove(path[:len(path)-1]...)
	}
	start, end := d.span(e)
	// Also drop one blank line after a removed top-level entry, so two
	// blank lines don't end up next to each other.
	if e.key.Column == 1 && end < len(d.lines) && strings.TrimSpace(d.lines[end]) == "" &&
		(start == 0 || strings.TrimSpace(d.lines[start-1]) == "") {
		end++
	}
	d.lines = append(d.lines[:start], d.lines[end:]...)
	return nil
}

// replaceToken replaces a scalar node's text on its line with s.
func (d *doc) replaceToken(n *yaml.Node, s string) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: not a single value", n.Line)
	}
	line := d.lines[n.Line-1]
	start := n.Column - 1
	end, err := tokenEnd(line, start, n)
	if err != nil {
		return err
	}
	d.lines[n.Line-1] = line[:start] + s + line[end:]
	return nil
}

// tokenEnd finds where the scalar starting at start ends on the line.
func tokenEnd(line string, start int, n *yaml.Node) (int, error) {
	if start >= len(line) {
		return 0, fmt.Errorf("line %d: can't find the value", n.Line)
	}
	switch n.Style {
	case yaml.DoubleQuotedStyle, yaml.SingleQuotedStyle:
		q := line[start]
		for i := start + 1; i < len(line); i++ {
			if line[i] == '\\' && q == '"' {
				i++
				continue
			}
			if line[i] == q {
				if q == '\'' && i+1 < len(line) && line[i+1] == '\'' {
					i++
					continue
				}
				return i + 1, nil
			}
		}
		return 0, fmt.Errorf("line %d: unterminated quote", n.Line)
	case 0, yaml.FlowStyle:
		if !strings.HasPrefix(line[start:], n.Value) {
			return 0, fmt.Errorf("line %d: can't edit this value in place", n.Line)
		}
		return start + len(n.Value), nil
	}
	return 0, fmt.Errorf("line %d: can't edit a block value in place", n.Line)
}

// quote returns s as a YAML scalar, quoted when it needs to be.
func quote(s string) string {
	out, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Sprintf("%q", s)
	}
	return strings.TrimSuffix(string(out), "\n")
}
