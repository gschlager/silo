package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/provision"
)

func TestHerdrTerminalName(t *testing.T) {
	if got := herdrTerminalName("ra-Claude.dev-120000"); got != "ra-claude-dev-120000" {
		t.Errorf("got %q", got)
	}
	if got := herdrTerminalName(strings.Repeat("a", 40)); len(got) != 32 {
		t.Errorf("got %d chars", len(got))
	}
}

// TestHerdrLaunch runs the generated line with a fake hc on PATH, and without.
func TestHerdrLaunch(t *testing.T) {
	cfg := &config.MergedConfig{ProjectDir: "/home/me/myapp", ProjectKey: "myapp", Shell: "sh"}
	line := herdrLaunch(cfg, "claude", "claude 'fix it'") + "echo direct"

	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(dir, "hc"), []byte(fake), 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", "-c", line)
	cmd.Env = append(os.Environ(), "PATH="+dir+":/usr/bin:/bin")
	if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "direct") {
		t.Fatalf("with hc: err=%v out=%s", err, out)
	}
	calls, _ := os.ReadFile(log)
	got := string(calls)
	for _, want := range []string{
		"run --bg --rm --no-parent --name ra-claude-",
		"--cwd /workspace/myapp --label herdr.space=myapp",
		"-- /bin/sh -lc claude 'fix it'",
		"\nattach ra-claude-",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("hc calls missing %q:\n%s", want, got)
		}
	}

	cmd = exec.Command("sh", "-c", line)
	cmd.Env = append(os.Environ(), "PATH=/usr/bin:/bin")
	if out, err := cmd.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "direct" {
		t.Fatalf("without hc: err=%v out=%s", err, out)
	}
}

func TestIsHerdrVersion(t *testing.T) {
	for out, want := range map[string]bool{
		"hc 0.1.17\n":         true,
		"hc 1.0":              true,
		"":                    false,
		"hc version unknown":  false,
		"HashiCorp hc v2.0.1": false,
	} {
		if got := provision.IsHerdrVersion(out); got != want {
			t.Errorf("isHerdrVersion(%q) = %v, want %v", out, got, want)
		}
	}
}
