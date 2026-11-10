package configupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const projectYAML = `# my project
image: fedora/44   # pinned for pg

docker: true

# host data
mounts:
  - ~/datasets:/data:ro

git:
  user.email: me@example.com
  credential:
    source: 1password
    ref: op://Work/gh/token

tools:
  npm:
    credential:
      source: token
      value: npm_secret

deamons:
  web:
    cmd: bin/rails s

wat: 1
`

func setup(t *testing.T, project string) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	os.MkdirAll(filepath.Join(cfg, "silo"), 0700)
	os.WriteFile(filepath.Join(cfg, "silo", "config.yml"), []byte("default_image: fedora/45\n\n# mounts for one project\nproject_mounts:\n  other-1234:\n    - ~/x:/x\n"), 0600)
	os.WriteFile(filepath.Join(cfg, "silo", "secrets.yml"), []byte("# secrets\n\nmyapp:\n  # github: op://vault/item/field\n\nother:\n  github: op://a/b/c\n"), 0600)
	dir := filepath.Join(t.TempDir(), "myapp")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, ".silo.yml"), []byte(project), 0644)
	return dir
}

func byID(t *testing.T, ss []Suggestion, prefix string) Suggestion {
	t.Helper()
	for _, s := range ss {
		if strings.HasPrefix(s.ID, prefix+":") {
			return s
		}
	}
	t.Fatalf("no %s suggestion in %v", prefix, ids(ss))
	return Suggestion{}
}

func ids(ss []Suggestion) []string {
	var out []string
	for _, s := range ss {
		out = append(out, strings.Split(s.ID, ":")[0])
	}
	return out
}

func TestCheckFindsEverything(t *testing.T) {
	dir := setup(t, projectYAML)
	ss, err := Check(dir, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"image", "mounts", "credential-git.credential", "credential-tools.npm.credential", "unknown-docker", "unknown-deamons", "unknown-wat"}
	if got := ids(ss); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("suggestions = %v, want %v", got, want)
	}

	img := byID(t, ss, "image")
	if !strings.Contains(img.After, "image: fedora/45   # pinned for pg\n") {
		t.Errorf("image keeps its comment:\n%s", img.After)
	}

	m := byID(t, ss, "mounts")
	if strings.Contains(m.After, "mounts") || strings.Contains(m.After, "datasets") || !strings.Contains(m.After, "docker: true\n\ngit:") {
		t.Errorf("mounts removed cleanly, with its comment:\n%s", m.After)
	}
	g := m.Others[0].After
	if !strings.Contains(g, "project_mounts:\n  other-1234:\n    - ~/x:/x\n  myapp-") || !strings.Contains(g, "    - ~/datasets:/data:ro") {
		t.Errorf("global config:\n%s", g)
	}

	gc := byID(t, ss, "credential-git.credential")
	if strings.Contains(gc.After, "credential:\n    source: 1password") || !strings.Contains(gc.After, "git:\n  user.email: me@example.com\n\ntools:") {
		t.Errorf("git.credential removed, git: kept:\n%s", gc.After)
	}
	sec := gc.Others[0].After
	if !strings.Contains(sec, "myapp:\n  github: op://Work/gh/token\n  # github: op://vault/item/field\n") {
		t.Errorf("secrets.yml:\n%s", sec)
	}

	npm := byID(t, ss, "credential-tools.npm.credential")
	if strings.Contains(npm.After, "tools:") || strings.Contains(npm.After, "npm_secret") {
		t.Errorf("the whole tools: block goes when npm was its only entry:\n%s", npm.After)
	}
	if !strings.Contains(npm.Detail, "rotate") || !strings.Contains(npm.Others[0].After, "NPM_TOKEN: npm_secret") {
		t.Errorf("npm: %s\n%s", npm.Detail, npm.Others[0].After)
	}

	if d := byID(t, ss, "unknown-docker"); !strings.Contains(d.After, "\nnesting: true\n") {
		t.Errorf("docker -> nesting:\n%s", d.After)
	}
	if d := byID(t, ss, "unknown-deamons"); !strings.Contains(d.After, "\ndaemons:\n  web:") {
		t.Errorf("deamons -> daemons:\n%s", d.After)
	}
	if w := byID(t, ss, "unknown-wat"); !w.Careful || strings.Contains(w.After, "wat") {
		t.Errorf("wat: careful=%v\n%s", w.Careful, w.After)
	}
}

func TestApplyAllInTurn(t *testing.T) {
	dir := setup(t, projectYAML)
	// The CLI applies one suggestion and checks again; do the same.
	for range 20 {
		ss, err := Check(dir, "myapp")
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) == 0 {
			break
		}
		if err := ss[0].Apply(); err != nil {
			t.Fatalf("%s: %v", ss[0].ID, err)
		}
	}
	got, _ := os.ReadFile(filepath.Join(dir, ".silo.yml"))
	want := `# my project
image: fedora/45   # pinned for pg

nesting: true

git:
  user.email: me@example.com

daemons:
  web:
    cmd: bin/rails s
`
	if string(got) != want {
		t.Errorf("result:\n%s\nwant:\n%s", got, want)
	}
	if ss, _ := Check(dir, "myapp"); len(ss) != 0 {
		t.Errorf("still suggesting %v", ids(ss))
	}
	sec, _ := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "silo", "secrets.yml"))
	if fi, _ := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "silo", "secrets.yml")); fi.Mode().Perm() != 0600 {
		t.Errorf("secrets.yml mode %v", fi.Mode().Perm())
	}
	if !strings.Contains(string(sec), "NPM_TOKEN: npm_secret") || !strings.Contains(string(sec), "github: op://Work/gh/token") {
		t.Errorf("secrets.yml:\n%s", sec)
	}
}

func TestNothingToDo(t *testing.T) {
	dir := setup(t, "image: fedora/45\nsetup:\n  - make\n")
	if ss, err := Check(dir, "myapp"); err != nil || len(ss) != 0 {
		t.Errorf("suggestions %v, %v", ids(ss), err)
	}
}

func TestSecretConflict(t *testing.T) {
	src := "myapp:\n  github: op://x/y/z\n"
	if _, err := withSecret(src, "myapp", "github", "op://other"); err == nil {
		t.Error("overwrote a different github secret")
	}
	if out, err := withSecret(src, "myapp", "github", "op://x/y/z"); err != nil || out != src {
		t.Errorf("same value: %q %v", out, err)
	}
}

func TestOlderRelease(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{{"44", "45", true}, {"45", "45", false}, {"46", "45", false}, {"22.04", "24.04", true}, {"rawhide", "45", false}} {
		if got := olderRelease(tt.a, tt.b); got != tt.want {
			t.Errorf("olderRelease(%s, %s) = %v", tt.a, tt.b, got)
		}
	}
}

func TestDiff(t *testing.T) {
	d := Diff("a\nb\nc\n", "a\nB\nc\n", 1)
	var s []string
	for _, l := range d {
		s = append(s, string(l.Op)+l.Text)
	}
	if strings.Join(s, "|") != " a|-b|+B| c" {
		t.Errorf("diff = %v", s)
	}
}

func TestApplyAllLeavesCarefulOnes(t *testing.T) {
	dir := setup(t, projectYAML)
	applied, left, err := ApplyAll(dir, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if applied == 0 {
		t.Fatal("applied nothing")
	}
	for _, s := range left {
		if !s.Careful {
			t.Errorf("left a suggestion it should have applied: %s", s.Title)
		}
	}
	if len(left) == 0 {
		t.Error("expected the unknown key wat to be left for the terminal")
	}
	ss, err := Check(dir, "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != len(left) {
		t.Errorf("after ApplyAll: %v, want only the careful ones", ids(ss))
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".silo.yml"))
	if !strings.Contains(string(data), "fedora/45") || !strings.Contains(string(data), "nesting: true") {
		t.Errorf(".silo.yml after ApplyAll:\n%s", data)
	}
}
