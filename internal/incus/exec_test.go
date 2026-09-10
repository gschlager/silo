package incus

import (
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSuUserManager(t *testing.T) {
	cmd := SuUserManager("dev", "systemctl --user daemon-reload")
	want := []string{"su", "-", "dev", "-c", UserManagerPrefix + "systemctl --user daemon-reload"}
	if len(cmd) != len(want) {
		t.Fatalf("SuUserManager() = %q, want %q", cmd, want)
	}
	for i := range want {
		if cmd[i] != want[i] {
			t.Errorf("SuUserManager()[%d] = %q, want %q", i, cmd[i], want[i])
		}
	}
	// Reaching the user manager's bus needs both halves of the prefix: the
	// runtime dir and the wait for its socket.
	if !strings.Contains(cmd[4], "XDG_RUNTIME_DIR=/run/user/$(id -u)") {
		t.Errorf("command does not set XDG_RUNTIME_DIR: %q", cmd[4])
	}
	if !strings.Contains(cmd[4], `[ ! -S "$XDG_RUNTIME_DIR/bus" ]`) {
		t.Errorf("command does not wait for the user bus socket: %q", cmd[4])
	}
}

// TestUserManagerPrefixShell runs the prefix through a real shell: it must pass
// straight through once the bus socket is there, and give up after a bounded
// wait when it never appears, rather than hanging.
func TestUserManagerPrefixShell(t *testing.T) {
	runDir := t.TempDir()
	// Point the prefix at a temp dir instead of /run/user/<uid>, and shorten the
	// wait so the missing-socket case doesn't sit here for 15 seconds.
	script := strings.Replace(UserManagerPrefix, "/run/user/$(id -u)", runDir, 1)
	script = strings.Replace(script, "-lt "+userManagerWaitTries, "-lt 2", 1)

	run := func(t *testing.T) time.Duration {
		t.Helper()
		start := time.Now()
		out, err := exec.Command("sh", "-c", script+"echo ready").CombinedOutput()
		if err != nil {
			t.Fatalf("running prefix: %v (output: %s)", err, out)
		}
		if strings.TrimSpace(string(out)) != "ready" {
			t.Errorf("prefix output = %q, want %q", out, "ready")
		}
		return time.Since(start)
	}

	t.Run("socket missing", func(t *testing.T) {
		if elapsed := run(t); elapsed > 5*time.Second {
			t.Errorf("waited %s for a socket that never appears", elapsed)
		}
	})

	t.Run("socket present", func(t *testing.T) {
		listener, err := net.Listen("unix", filepath.Join(runDir, "bus"))
		if err != nil {
			t.Skipf("cannot create unix socket: %v", err)
		}
		defer listener.Close()
		if elapsed := run(t); elapsed > time.Second {
			t.Errorf("waited %s with the socket already there", elapsed)
		}
	})
}
