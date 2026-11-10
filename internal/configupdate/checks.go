// Package configupdate finds things in a project's .silo.yml (and
// .silo.local.yml) that are out of date with this silo version, and changes
// them when you agree. `silo config update` runs it; `silo up` mentions it
// when there's something to update.
package configupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/gschlager/silo/internal/config"
	"gopkg.in/yaml.v3"
)

// Suggestion is one proposed change to a project file.
type Suggestion struct {
	// ID identifies the suggestion across runs (rule and file), so one you
	// declined isn't asked again in the same session.
	ID     string
	File   string // the project file it changes
	Title  string // one line
	Detail string // why, and what else it changes
	// Before and After are the project file's content, for showing a diff.
	Before, After string
	// Others are other files it changes (secrets.yml, the global config),
	// with their new content.
	Others []FileChange
	// Careful is set for changes that drop something silo can't interpret,
	// so the default answer should be no.
	Careful bool
}

// FileChange is a file and its new content.
type FileChange struct {
	Path, Before, After string
	Mode                os.FileMode
}

// Apply writes the changes.
func (s Suggestion) Apply() error {
	for _, o := range s.Others {
		if err := writeAtomic(o.Path, o.After, o.Mode); err != nil {
			return err
		}
	}
	return writeAtomic(s.File, s.After, 0644)
}

// projectFiles are the files checked, the shared one first.
var projectFiles = [][]string{{".silo.yml", ".silo.yaml"}, {".silo.local.yml", ".silo.local.yaml"}}

// Check returns the suggestions for the project in dir. It reads files and
// changes nothing. projectKey is the project's name in secrets.yml (see
// config.AssignContainerName); it's only needed for moving credentials.
func Check(dir, projectKey string) ([]Suggestion, error) {
	global, err := config.LoadGlobalConfig()
	if err != nil {
		return nil, err
	}
	var out []Suggestion
	for _, names := range projectFiles {
		for _, name := range names {
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			c := &checker{dir: dir, path: path, name: name, src: string(data), global: global, projectKey: projectKey}
			d, err := parseDoc(c.src)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if d.root == nil {
				break
			}
			c.doc = d
			for _, rule := range []func(*checker) ([]Suggestion, error){
				(*checker).image,
				(*checker).mounts,
				(*checker).credentials,
				(*checker).unknownKeys,
			} {
				s, err := rule(c)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}
				out = append(out, s...)
			}
			break
		}
	}
	return out, nil
}

// ApplyAll applies the suggestions for the project in dir, except the careful
// ones, checking again after each change. It returns how many it applied and
// the suggestions it left alone.
func ApplyAll(dir, projectKey string) (applied int, left []Suggestion, err error) {
	done := map[string]bool{}
	for {
		all, err := Check(dir, projectKey)
		if err != nil {
			return applied, nil, err
		}
		left = nil
		var next *Suggestion
		for _, s := range all {
			switch {
			case s.Careful || done[s.ID]:
				left = append(left, s)
			case next == nil:
				next = &s
			}
		}
		if next == nil {
			return applied, left, nil
		}
		if err := next.Apply(); err != nil {
			return applied, nil, fmt.Errorf("%s: %w", next.Title, err)
		}
		done[next.ID] = true
		applied++
	}
}

type checker struct {
	dir, path, name, src string
	doc                  *doc
	global               *config.GlobalConfig
	projectKey           string
}

// edit returns the file content after fn changed a fresh copy of it.
func (c *checker) edit(fn func(*doc) error) (string, error) {
	d, err := parseDoc(c.src)
	if err != nil {
		return "", err
	}
	if err := fn(d); err != nil {
		return "", err
	}
	return d.text(), nil
}

func (c *checker) suggestion(id, title, detail string, fn func(*doc) error) (Suggestion, error) {
	after, err := c.edit(fn)
	if err != nil {
		return Suggestion{}, err
	}
	return Suggestion{ID: id + ":" + c.path, File: c.path, Title: title, Detail: detail, Before: c.src, After: after}, nil
}

// image: an older release of the default image's distribution.
func (c *checker) image() ([]Suggestion, error) {
	e, ok := c.doc.get("image")
	if !ok || e.val.Kind != yaml.ScalarNode {
		return nil, nil
	}
	cur, def := e.val.Value, c.global.DefaultImage
	curDistro, curRel := splitImage(cur)
	defDistro, defRel := splitImage(def)
	if curDistro == "" || curDistro != defDistro || !olderRelease(curRel, defRel) {
		return nil, nil
	}
	// Keep a remote prefix like "images:".
	updated := strings.TrimSuffix(cur, curRel) + defRel
	s, err := c.suggestion("image",
		fmt.Sprintf("Use %s instead of %s", updated, cur),
		fmt.Sprintf("%s is the default now. The container keeps its image until you recreate it (silo rm && silo up).", def),
		func(d *doc) error {
			e, _ := d.get("image")
			return d.replaceToken(e.val, updated)
		})
	return []Suggestion{s}, err
}

// splitImage splits "fedora/44" or "images:fedora/44" into distribution and
// release.
func splitImage(image string) (string, string) {
	if i := strings.IndexByte(image, ':'); i >= 0 {
		image = image[i+1:]
	}
	distro, rel, ok := strings.Cut(image, "/")
	if !ok {
		return "", ""
	}
	return distro, rel
}

// olderRelease compares numeric releases like "44" or "24.04"; anything
// else (rawhide, edge) is never older.
func olderRelease(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		var err error
		if i < len(pa) {
			if x, err = strconv.Atoi(pa[i]); err != nil {
				return false
			}
		}
		if i < len(pb) {
			if y, err = strconv.Atoi(pb[i]); err != nil {
				return false
			}
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// mounts: project files can't declare host mounts anymore; they go to the
// global config's project_mounts.
func (c *checker) mounts() ([]Suggestion, error) {
	e, ok := c.doc.get("mounts")
	if !ok {
		return nil, nil
	}
	var mounts []string
	if e.val.Kind == yaml.SequenceNode {
		for _, n := range e.val.Content {
			if n.Kind == yaml.ScalarNode {
				mounts = append(mounts, n.Value)
			}
		}
	}
	key := config.PathScopedProjectName(c.dir)
	s, err := c.suggestion("mounts",
		"Move mounts: to the global config",
		"Project files are writable from inside the container, so silo only takes host mounts from "+
			config.GlobalConfigPath()+" now, and refuses to start with mounts: in "+c.name+".",
		func(d *doc) error { return d.remove("mounts") })
	if err != nil {
		return nil, err
	}
	if len(mounts) > 0 {
		gpath := config.GlobalConfigPath()
		before, err := readOptional(gpath)
		if err != nil {
			return nil, err
		}
		after, err := withProjectMounts(before, key, mounts)
		if err != nil {
			return nil, err
		}
		s.Others = append(s.Others, FileChange{Path: gpath, Before: before, After: after, Mode: 0644})
	}
	return []Suggestion{s}, nil
}

// credentials: git.credential and tools.<name>.credential belong in
// secrets.yml, which isn't in the repo and which the agent can't edit.
func (c *checker) credentials() ([]Suggestion, error) {
	type cred struct {
		path    []string
		secret  string
		meaning string
	}
	var creds []cred
	if _, ok := c.doc.get("git", "credential"); ok {
		creds = append(creds, cred{[]string{"git", "credential"}, "github", "the reserved github key: GITHUB_TOKEN for git, and GH_TOKEN for gh too"})
	}
	if tools, ok := c.doc.get("tools"); ok {
		for _, t := range entries(tools.val) {
			if _, ok := c.doc.get("tools", t.key.Value, "credential"); !ok {
				continue
			}
			env := strings.ToUpper(t.key.Value) + "_TOKEN"
			creds = append(creds, cred{[]string{"tools", t.key.Value, "credential"}, env, "the same variable, " + env})
		}
	}

	var out []Suggestion
	for _, cr := range creds {
		e, _ := c.doc.get(cr.path...)
		fields := map[string]string{}
		for _, f := range entries(e.val) {
			fields[f.key.Value] = f.val.Value
		}
		var value, extra string
		switch {
		case fields["source"] == "1password" && fields["ref"] != "":
			value = fields["ref"]
		case fields["source"] == "token" && fields["value"] != "":
			value = fields["value"]
			extra = " The token is in this file as plain text, so it's probably in the repo's history too: rotate it."
		default:
			continue // an env: source can't be expressed in secrets.yml
		}
		if c.projectKey == "" {
			continue
		}
		where := strings.Join(cr.path, ".")
		s, err := c.suggestion("credential-"+where,
			"Move "+where+" to secrets.yml",
			fmt.Sprintf("secrets.yml isn't part of the repo and can't be changed from inside the container. It becomes %s in the %s entry, as %s.%s",
				cr.secret, c.projectKey, cr.meaning, extra),
			func(d *doc) error { return d.remove(cr.path...) })
		if err != nil {
			return nil, err
		}
		spath := config.SecretsPath()
		before, err := readOptional(spath)
		if err != nil {
			return nil, err
		}
		after, err := withSecret(before, c.projectKey, cr.secret, value)
		if err != nil {
			return nil, err
		}
		s.Others = append(s.Others, FileChange{Path: spath, Before: before, After: after, Mode: 0600})
		out = append(out, s)
	}
	return out, nil
}

// unknownKeys: top-level keys silo doesn't know (anymore). It ignores them,
// which hides typos and leftovers.
func (c *checker) unknownKeys() ([]Suggestion, error) {
	known := projectKeys()
	var out []Suggestion
	for _, e := range entries(c.doc.root) {
		key := e.key.Value
		if slices.Contains(known, key) {
			continue
		}
		var s Suggestion
		var err error
		switch {
		case key == "docker" && e.val.Value == "true":
			if _, has := c.doc.get("nesting"); has {
				s, err = c.suggestion("unknown-docker", "Remove docker: true",
					"docker: was replaced by nesting:, which this file already sets.",
					func(d *doc) error { return d.remove("docker") })
			} else {
				s, err = c.suggestion("unknown-docker", "Replace docker: true with nesting: true",
					"docker: was replaced by nesting: true, which lets you run Docker or Podman in the container. silo ignores docker:.",
					func(d *doc) error {
						e, _ := d.get("docker")
						return d.replaceToken(e.key, "nesting")
					})
			}
		case key == "docker" || key == "compose":
			s, err = c.suggestion("unknown-"+key, "Remove "+key+":",
				"silo dropped its Docker Compose support and ignores "+key+":. With nesting: true you can run docker compose yourself, e.g. as a daemon.",
				func(d *doc) error { return d.remove(key) })
		default:
			if near := closest(key, known); near != "" {
				if _, has := c.doc.get(near); !has {
					s, err = c.suggestion("unknown-"+key, fmt.Sprintf("Rename %s: to %s:", key, near),
						fmt.Sprintf("silo doesn't know %s: and ignores it. Did you mean %s:?", key, near),
						func(d *doc) error {
							e, _ := d.get(key)
							return d.replaceToken(e.key, near)
						})
					break
				}
			}
			s, err = c.suggestion("unknown-"+key, "Remove "+key+":",
				"silo doesn't know "+key+": and ignores it. Remove it only if it isn't meant for something else.",
				func(d *doc) error { return d.remove(key) })
			s.Careful = true
		}
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// projectKeys are the top-level keys of .silo.yml.
func projectKeys() []string {
	var keys []string
	t := reflect.TypeOf(config.ProjectConfig{})
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	return keys
}

// closest returns the known key within two edits of key, if there is one.
func closest(key string, known []string) string {
	best, bestDist := "", 3
	for _, k := range known {
		if d := editDistance(key, k); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
