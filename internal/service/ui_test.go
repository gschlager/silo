package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gschlager/silo/internal/provision"
)

func uiRequest(t *testing.T, h http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://silo.localhost"+path, strings.NewReader(""))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestUIGuard(t *testing.T) {
	s := newServer(testOptions())
	me := true
	old := peerIsMe
	peerIsMe = func(*http.Request) bool { return me }
	t.Cleanup(func() { peerIsMe = old })
	h := s.uiHandler()

	w := uiRequest(t, h, "GET", "/", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<title>silo</title>") {
		t.Fatalf("index: %d %q", w.Code, w.Body.String())
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", csp)
	}

	w = uiRequest(t, h, "GET", "/api/overview", map[string]string{"Sec-Fetch-Site": "same-origin"})
	if w.Code != 200 {
		t.Fatalf("overview: %d %s", w.Code, w.Body.String())
	}
	var o uiOverview
	if err := json.Unmarshal(w.Body.Bytes(), &o); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		method  string
		headers map[string]string
		want    int
	}{
		{"same-origin POST", "POST", map[string]string{"Sec-Fetch-Site": "same-origin", "X-Silo-UI": "1"}, 200},
		{"POST without the header", "POST", map[string]string{"Sec-Fetch-Site": "same-origin"}, 403},
		{"cross-site POST", "POST", map[string]string{"Sec-Fetch-Site": "cross-site", "X-Silo-UI": "1"}, 403},
		{"POST from a container app", "POST", map[string]string{"Sec-Fetch-Site": "same-site", "X-Silo-UI": "1"}, 403},
		{"foreign Origin", "POST", map[string]string{"Origin": "http://myapp.localhost", "X-Silo-UI": "1"}, 403},
		{"own Origin", "POST", map[string]string{"Origin": "https://silo.localhost:7443", "X-Silo-UI": "1"}, 200},
		{"cross-site GET", "GET", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
	}
	for _, tt := range tests {
		path := "/api/secrets/forget"
		if tt.method == "GET" {
			path = "/api/overview"
		}
		if w := uiRequest(t, h, tt.method, path, tt.headers); w.Code != tt.want {
			t.Errorf("%s: status %d, want %d", tt.name, w.Code, tt.want)
		}
	}

	me = false
	if w := uiRequest(t, h, "GET", "/", nil); w.Code != 403 {
		t.Errorf("another user: status %d, want 403", w.Code)
	}
}

func TestProxyServesUIAtSiloLocalhost(t *testing.T) {
	s := newServer(testOptions())
	old := peerIsMe
	peerIsMe = func(*http.Request) bool { return true }
	t.Cleanup(func() { peerIsMe = old })
	w := httptest.NewRecorder()
	s.proxy.ServeHTTP(w, httptest.NewRequest("GET", "http://silo.localhost/app.js", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "textContent") {
		t.Errorf("app.js: %d", w.Code)
	}
}

func TestOutdated(t *testing.T) {
	cur := strconv.Itoa(provision.ContainerVersion)
	old := strconv.Itoa(provision.ContainerVersion - 1)
	for stamp, want := range map[string]bool{"": false, cur: false, old: true, "junk": false} {
		if got := outdated(stamp); got != want {
			t.Errorf("outdated(%q) = %v, want %v", stamp, got, want)
		}
	}
}
