package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gschlager/silo/internal/config"
)

func TestHostURL(t *testing.T) {
	ports := []config.PortForward{
		{Spec: "3000"},
		{Spec: "5432:15432"},
	}
	tests := []struct {
		in, want, err string
	}{
		{in: "https://github.com/owner/repo/pull/1", want: "https://github.com/owner/repo/pull/1"},
		{in: "http://localhost:3000/admin?x=1", want: "http://localhost:3000/admin?x=1"},
		{in: "http://127.0.0.1:5432/", want: "http://localhost:15432/"},
		{in: "http://0.0.0.0:3000", want: "http://localhost:3000"},
		{in: "http://localhost:4000/", err: "isn't forwarded"},
		{in: "https://myapp.localhost/x", want: "https://myapp.localhost/x"},
		{in: "file:///etc/passwd", err: "only http and https"},
		{in: "javascript:alert(1)", err: "only http and https"},
		{in: "ssh://example.com", err: "only http and https"},
		{in: "https://user:pw@example.com/", err: "no user info"},
		{in: "https:///nohost", err: "needs a host"},
	}
	for _, tt := range tests {
		got, err := hostURL(tt.in, ports, nil)
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("hostURL(%q) error = %v, want %q", tt.in, err, tt.err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("hostURL(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestHostURLPrefersProxy(t *testing.T) {
	proxy := func(cport int, _ []config.PortForward) string {
		if cport == 3000 {
			return "https://myapp.localhost"
		}
		return ""
	}
	got, err := hostURL("http://localhost:3000/a?b=c", []config.PortForward{{Spec: "3000", HTTP: true}}, proxy)
	if err != nil || got != "https://myapp.localhost/a?b=c" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestOpenNeedsAClick(t *testing.T) {
	s := &Server{containerConfig: func(string) (*config.MergedConfig, error) { return &config.MergedConfig{}, nil }}

	var mu sync.Mutex
	var opened []string
	clicked := make(chan bool, 1)
	oldConfirm, oldOpen := confirmOpen, openInBrowser
	confirmOpen = func(ctx context.Context, title, target string) bool { return <-clicked }
	openInBrowser = func(u string) { mu.Lock(); opened = append(opened, u); mu.Unlock() }
	t.Cleanup(func() { confirmOpen, openInBrowser = oldConfirm, oldOpen })

	post := func(body string) int {
		w := httptest.NewRecorder()
		s.handleOpen("silo-demo", w, httptest.NewRequest("POST", "/v1/open", strings.NewReader(body)))
		return w.Code
	}

	if code := post("https://example.com/"); code != http.StatusAccepted {
		t.Fatalf("status = %d", code)
	}
	// A second request while the first waits for its click is refused.
	if code := post("https://example.org/"); code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", code)
	}
	clicked <- false // dismissed
	time.Sleep(50 * time.Millisecond)

	if code := post("https://example.com/"); code != http.StatusAccepted {
		t.Fatalf("status = %d", code)
	}
	clicked <- true
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(opened) != 1 || opened[0] != "https://example.com/" {
		t.Errorf("opened = %v, want only the clicked one", opened)
	}
	if code := post("file:///etc/shadow"); code != http.StatusBadRequest {
		t.Errorf("file URL status = %d", code)
	}
}
