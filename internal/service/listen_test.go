package service

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListenUnixLongPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("d", 60), strings.Repeat("e", 60))
	path := filepath.Join(dir, "ssh-agent.sock")
	if len(path) <= maxSocketPath {
		t.Fatalf("test path too short: %d", len(path))
	}
	l, err := listenUnix(path, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	fi, err := os.Stat(path)
	if err != nil || fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0600 {
		t.Fatalf("socket at the long path: %v %v", fi, err)
	}
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
		}
	}()
	// Clients have the same limit, so connect the same way.
	d, _ := os.Open(dir)
	defer d.Close()
	c, err := net.Dial("unix", fmt.Sprintf("/proc/self/fd/%d/ssh-agent.sock", d.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestNotifyLimiter(t *testing.T) {
	var l notifyLimiter
	if !l.allow("silo-a") {
		t.Fatal("first notification refused")
	}
	if l.allow("silo-a") {
		t.Error("second notification right away allowed")
	}
	if !l.allow("silo-b") {
		t.Error("another container's notification refused")
	}
	l.last["silo-a"] = time.Now().Add(-notifyInterval)
	if !l.allow("silo-a") {
		t.Error("notification after the interval refused")
	}
}
