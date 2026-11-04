package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTmpTarget(t *testing.T) {
	cwd := t.TempDir()
	sub := filepath.Join(cwd, "other")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	issuePrompt := "Look at https://github.com/markdown-it/linkify-it/issues/126 and help me work on it. The repository is checked out in the current directory."
	cases := []struct {
		arg  string
		want tmpTarget
	}{
		{"", tmpTarget{Dir: cwd, Name: filepath.Base(cwd)}},
		{"other", tmpTarget{Dir: sub, Name: "other"}},
		{"markdown-it/linkify-it", tmpTarget{Clone: "https://github.com/markdown-it/linkify-it.git", Name: "linkify-it"}},
		{"https://github.com/markdown-it/linkify-it", tmpTarget{Clone: "https://github.com/markdown-it/linkify-it.git", Name: "linkify-it"}},
		{"https://github.com/markdown-it/linkify-it.git", tmpTarget{Clone: "https://github.com/markdown-it/linkify-it.git", Name: "linkify-it"}},
		{"https://github.com/markdown-it/linkify-it/tree/master/lib", tmpTarget{Clone: "https://github.com/markdown-it/linkify-it.git", Name: "linkify-it"}},
		{"https://github.com/markdown-it/linkify-it/issues/126", tmpTarget{Clone: "https://github.com/markdown-it/linkify-it.git", Name: "linkify-it", Prompt: issuePrompt}},
		{"github.com/markdown-it/linkify-it/issues/126#issuecomment-1", tmpTarget{Clone: "https://github.com/markdown-it/linkify-it.git", Name: "linkify-it", Prompt: issuePrompt}},
		{"https://github.com/a/b.js/pull/7", tmpTarget{Clone: "https://github.com/a/b.js.git", Name: "b.js",
			Prompt: "Look at https://github.com/a/b.js/pull/7 and help me work on it. The repository is checked out in the current directory."}},
		{"https://gitlab.com/group/proj.git", tmpTarget{Clone: "https://gitlab.com/group/proj.git", Name: "proj"}},
		{"git@codeberg.org:me/thing.git", tmpTarget{Clone: "git@codeberg.org:me/thing.git", Name: "thing"}},
	}
	for _, c := range cases {
		got, err := parseTmpTarget(c.arg, cwd)
		if err != nil {
			t.Errorf("parseTmpTarget(%q): %v", c.arg, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseTmpTarget(%q) = %+v, want %+v", c.arg, got, c.want)
		}
	}

	if _, err := parseTmpTarget("not-a-dir", cwd); err == nil {
		t.Errorf("expected an error for a name that's neither a dir nor a repo")
	}
}

func TestTmpContainerName(t *testing.T) {
	a, b := tmpContainerName("linkify-it"), tmpContainerName("linkify-it")
	if !strings.HasPrefix(a, "silo-tmp-linkify-it-") || len(a) != len("silo-tmp-linkify-it-")+4 {
		t.Errorf("got %q", a)
	}
	if a == b {
		t.Errorf("two names are the same: %q", a)
	}
	if long := tmpContainerName(strings.Repeat("x", 80)); len(long) > 63 {
		t.Errorf("name too long: %q (%d)", long, len(long))
	}
}
