package provision

import (
	"testing"

	"github.com/gschlager/silo/internal/config"
)

func TestBaseName(t *testing.T) {
	cases := map[string]string{
		"fedora/44":           "silo-base-fedora-44",
		"images:ubuntu/24.04": "silo-base-ubuntu-24-04",
		"Debian/12/cloud":     "silo-base-debian-12-cloud",
	}
	for image, want := range cases {
		if got := BaseName(image); got != want {
			t.Errorf("BaseName(%q) = %q, want %q", image, got, want)
		}
	}
	long := BaseName("images:averyveryveryveryveryveryveryveryverylongdistro/123456789")
	if len(long+"-build") > 63 {
		t.Errorf("BaseName too long for the -build suffix: %q", long)
	}
}

func testMerged(project *config.ProjectConfig) *config.MergedConfig {
	global := &config.GlobalConfig{
		Shell: "zsh", User: "dev", DefaultImage: "fedora/44",
		DefaultSetup: []string{"dnf install -y git"},
		Agents: []config.AgentGlobalConfig{
			{Name: "claude", Install: "curl claude | sh", Enabled: true},
			{Name: "codex", Install: "npm i -g codex", Enabled: true},
			{Name: "gemini", Install: "npm i -g gemini", Enabled: false},
		},
	}
	return config.Merge(global, project, "/tmp/proj")
}

// Projects that only differ in project-level settings share one template, even
// when they disable a global agent or enable an extra one.
func TestBaseSpecSharedAcrossProjects(t *testing.T) {
	disabled := false
	a := BaseSpecFor(testMerged(nil))
	b := BaseSpecFor(testMerged(&config.ProjectConfig{
		Setup:  []string{"bundle install"},
		Ports:  []config.PortForward{{Spec: "3000"}},
		Agents: map[string]config.AgentProjectConfig{"codex": {Enabled: &disabled}},
	}))
	if a.Hash() != b.Hash() {
		t.Errorf("project-level settings changed the base hash")
	}
	if len(a.Agents) != 2 || a.Agents["gemini"].Install != "" {
		t.Errorf("base agents = %v, want claude and codex", a.agentNames())
	}
}

func TestBaseSpecHashChanges(t *testing.T) {
	base := BaseSpecFor(testMerged(nil))
	h := base.Hash()

	img := base
	img.Image = "fedora/45"
	setup := base
	setup.DefaultSetup = []string{"dnf install -y git vim"}
	shell := base
	shell.Shell = "bash"
	ag := BaseSpecFor(testMerged(nil))
	a := ag.Agents["claude"]
	a.Install = "curl claude-v2 | sh"
	ag.Agents["claude"] = a

	for name, s := range map[string]BaseSpec{"image": img, "default_setup": setup, "shell": shell, "agent install": ag} {
		if s.Hash() == h {
			t.Errorf("changing %s didn't change the hash", name)
		}
	}
	if BaseSpecFor(testMerged(nil)).Hash() != h {
		t.Errorf("hash isn't stable")
	}
}

func TestAgentsNotIn(t *testing.T) {
	all := map[string]config.MergedAgentConfig{
		"claude": {Enabled: true},
		"codex":  {Enabled: false},
		"pi":     {Enabled: true},
	}
	got := agentsNotIn(all, map[string]bool{"claude": true, "codex": true})
	if len(got) != 1 {
		t.Fatalf("got %v, want only pi", got)
	}
	if _, ok := got["pi"]; !ok {
		t.Errorf("got %v, want only pi", got)
	}
}
