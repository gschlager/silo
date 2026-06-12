package presets

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/gschlager/silo/internal/config"
)

// parseUse parses a `.silo.yml`-style document and returns its ordered use list.
func parseUse(t *testing.T, doc string) config.UseList {
	t.Helper()
	var pc config.ProjectConfig
	if err := yaml.Unmarshal([]byte(doc), &pc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return pc.Use
}

func expand(t *testing.T, doc string) []string {
	t.Helper()
	cmds, err := Expand(parseUse(t, doc), "bash")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	return cmds
}

func contains(cmds []string, substr string) bool {
	for _, c := range cmds {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func TestRubyMRIVersionsAndPin(t *testing.T) {
	cmds := expand(t, `
use:
  ruby:
    versions: [3.3, 3.4, "4.0"]
    default: "4.0"
`)
	for _, want := range []string{
		"curl -LsSf https://rv.dev/install",
		"rv ruby install 3.3",
		"rv ruby install 3.4",
		"rv ruby install 4.0",
		"rv ruby pin 4.0",
		"rv shell init",
	} {
		if !contains(cmds, want) {
			t.Errorf("expected a command containing %q, got:\n%s", want, strings.Join(cmds, "\n"))
		}
	}
}

// The activation line lands in ~/.silo/env.sh, which non-interactive bash
// re-sources via BASH_ENV — including shells running under `set -u`, where a
// bare $ZSH_VERSION reference would abort the shell.
func TestRubyActivationSetUSafe(t *testing.T) {
	cmds := expand(t, `
use:
  ruby:
    versions: ["3.4"]
`)
	if !contains(cmds, "${ZSH_VERSION-}") {
		t.Errorf("expected set -u safe ${ZSH_VERSION-} in activation, got:\n%s", strings.Join(cmds, "\n"))
	}
	if contains(cmds, `"$ZSH_VERSION"`) {
		t.Errorf("found bare $ZSH_VERSION reference, unsafe under set -u:\n%s", strings.Join(cmds, "\n"))
	}
}

// 4.0 written without quotes parses as a YAML float; it must keep its exact
// text rather than collapsing to "4".
func TestRubyVersionFloatPreserved(t *testing.T) {
	cmds := expand(t, `
use:
  ruby:
    versions: [4.0]
`)
	if !contains(cmds, "rv ruby install 4.0") {
		t.Errorf("expected 'rv ruby install 4.0', got:\n%s", strings.Join(cmds, "\n"))
	}
	if contains(cmds, "rv ruby install 4\n") || contains(cmds, "install 4 ") {
		t.Errorf("version 4.0 was coerced to an integer:\n%s", strings.Join(cmds, "\n"))
	}
}

func TestRubyDefaultsToLastMRI(t *testing.T) {
	cmds := expand(t, `
use:
  ruby:
    versions: [3.3, 3.4]
`)
	if !contains(cmds, "rv ruby pin 3.4") {
		t.Errorf("expected default pin to last MRI (3.4), got:\n%s", strings.Join(cmds, "\n"))
	}
}

func TestRubyJRubyRouting(t *testing.T) {
	cmds := expand(t, `
use:
  ruby:
    versions: [3.4, jruby]
`)
	for _, want := range []string{
		"rv ruby install 3.4",     // MRI via rv
		"ruby-install jruby",      // jruby via ruby-install
		"java-latest-openjdk",     // JDK for jruby
		"rv ruby pin 3.4",         // pin the only MRI version
	} {
		if !contains(cmds, want) {
			t.Errorf("expected a command containing %q, got:\n%s", want, strings.Join(cmds, "\n"))
		}
	}
	// rv must not try to pin jruby.
	if contains(cmds, "rv ruby pin jruby") {
		t.Errorf("rv should not pin an alt ruby:\n%s", strings.Join(cmds, "\n"))
	}
}

func TestRubyVersionedJRuby(t *testing.T) {
	cmds := expand(t, `
use:
  ruby:
    versions: [jruby-9.4]
`)
	if !contains(cmds, "ruby-install jruby 9.4") {
		t.Errorf("expected 'ruby-install jruby 9.4', got:\n%s", strings.Join(cmds, "\n"))
	}
}

func TestRubyRequiresVersions(t *testing.T) {
	_, err := Expand(parseUse(t, "use:\n  ruby:\n"), "bash")
	if err == nil || !strings.Contains(err.Error(), "versions") {
		t.Errorf("expected a 'versions' error, got %v", err)
	}
}

func TestUnknownPreset(t *testing.T) {
	_, err := Expand(parseUse(t, "use:\n  bogus:\n"), "bash")
	if err == nil || !strings.Contains(err.Error(), "unknown preset") {
		t.Errorf("expected an 'unknown preset' error, got %v", err)
	}
}

// Presets expand in declaration order so dependencies (ruby before bundle, etc.)
// hold.
func TestExpandPreservesOrder(t *testing.T) {
	cmds := expand(t, `
use:
  ruby:
    versions: ["3.4"]
  node:
`)
	rubyIdx, nodeIdx := -1, -1
	for i, c := range cmds {
		if strings.Contains(c, "rv ruby install") && rubyIdx == -1 {
			rubyIdx = i
		}
		if strings.Contains(c, "dnf install -y nodejs") && nodeIdx == -1 {
			nodeIdx = i
		}
	}
	if rubyIdx == -1 || nodeIdx == -1 || rubyIdx > nodeIdx {
		t.Errorf("expected ruby before node (ruby=%d node=%d):\n%s", rubyIdx, nodeIdx, strings.Join(cmds, "\n"))
	}
}
