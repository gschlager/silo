package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gschlager/silo/internal/svcapi"
)

// startTestServer runs the service with its runtime dir in a temp dir and
// returns a connected client.
func startTestServer(t *testing.T) *svcapi.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "silo-svc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", dir+"/config")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWith(ctx, testOptions()) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("service didn't stop")
		}
	})

	for range 100 {
		if c, err := svcapi.Dial(); err == nil {
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("service didn't come up")
	return nil
}

func TestStatusAndShutdown(t *testing.T) {
	c := startTestServer(t)
	if c.Version != svcapi.Version {
		t.Errorf("version = %q, want %q", c.Version, svcapi.Version)
	}
	var st svcapi.StatusInfo
	if err := c.Do(context.Background(), "GET", "/v1/status", nil, &st); err != nil {
		t.Fatal(err)
	}
	if st.PID != os.Getpid() {
		t.Errorf("pid = %d", st.PID)
	}
	if err := c.Do(context.Background(), "POST", "/v1/shutdown", nil, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := svcapi.Dial(); err == nil {
		t.Error("service still answers after shutdown")
	}
}

func TestSocketMode(t *testing.T) {
	startTestServer(t)
	fi, err := os.Stat(svcapi.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("socket mode = %v", fi.Mode().Perm())
	}
}

func testOptions() Options {
	return Options{NoIncus: true, NoDBus: true, NoProxy: true}
}
