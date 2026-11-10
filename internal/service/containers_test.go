package service

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gschlager/silo/internal/provision"
)

// containerClient is an HTTP client for a container's API socket.
func containerClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
}

func writeGlobalConfig(t *testing.T, yaml string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "silo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
}

type caughtNotification struct{ title, body string }

func catchNotifications(t *testing.T) func() []caughtNotification {
	var mu sync.Mutex
	var got []caughtNotification
	old := desktopNotify
	desktopNotify = func(_ context.Context, title, body string) {
		mu.Lock()
		got = append(got, caughtNotification{title, body})
		mu.Unlock()
	}
	t.Cleanup(func() { desktopNotify = old })
	return func() []caughtNotification {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		return append([]caughtNotification(nil), got...)
	}
}

func TestContainerNotify(t *testing.T) {
	startTestServer(t)
	s := &Server{}
	socks, err := s.openContainerSockets("silo-demo")
	if err != nil {
		t.Fatal(err)
	}
	defer socks.close()

	fi, err := os.Stat(provision.HostAPISocket("silo-demo"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("api.sock mode = %v", fi.Mode().Perm())
	}

	got := catchNotifications(t)
	c := containerClient(provision.HostAPISocket("silo-demo"))
	post := func() {
		resp, err := c.Post("http://silo/v1/notify", "text/plain", strings.NewReader("claude needs input\n"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	writeGlobalConfig(t, "notifications: false\nherdr: false\n")
	post()
	if n := got(); len(n) != 0 {
		t.Fatalf("notified with notifications off: %v", n)
	}

	writeGlobalConfig(t, "notifications: true\nherdr: false\n")
	post()
	n := got()
	if len(n) != 1 || n[0].title != "silo: demo" || n[0].body != "claude needs input" {
		t.Fatalf("notifications = %+v", n)
	}
}
