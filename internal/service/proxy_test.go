package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/localca"
)

// testProxy returns a proxy with one route, myapp.localhost, to an upstream
// test server, and that server's received Host headers.
func testProxy(t *testing.T) (*proxy, chan string) {
	t.Helper()
	hosts := make(chan string, 10)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts <- r.Host + " " + r.Header.Get("X-Forwarded-Host") + " " + r.URL.RequestURI()
		io.WriteString(w, "hello from the app")
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	port, _ := strconv.Atoi(u.Port())

	s := &Server{}
	p := newProxy(s)
	s.proxy = p
	p.routes = map[string]proxyRoute{
		"myapp.localhost": {container: "silo-myapp", HTTPRoute: config.HTTPRoute{Host: "myapp.localhost", ContainerPort: 3000, HostPort: port}},
	}
	p.built = time.Now()
	p.httpPort = 80
	return p, hosts
}

func TestProxyRoutesByHost(t *testing.T) {
	p, hosts := testProxy(t)

	req := httptest.NewRequest("GET", "http://myapp.localhost/admin?x=1", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "hello from the app" {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
	if got := <-hosts; got != "myapp.localhost myapp.localhost /admin?x=1" {
		t.Errorf("upstream saw %q, want the .localhost Host kept", got)
	}
	if p.s.activity.lastUsed("silo-myapp").IsZero() {
		t.Error("a request didn't count as activity")
	}

	// With a port in the Host header (fallback ports).
	w = httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "http://MyApp.localhost:7080/", nil))
	if w.Code != 200 {
		t.Errorf("Host with port: status %d", w.Code)
	}

	w = httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "http://other.localhost/", nil))
	if w.Code != 404 || !strings.Contains(w.Body.String(), "myapp.localhost") {
		t.Errorf("unknown host: %d %q", w.Code, w.Body.String())
	}
}

func TestProxyAppDown(t *testing.T) {
	p, _ := testProxy(t)
	p.dial = func(context.Context, string) error { return &net.OpError{Op: "dial"} }
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "http://myapp.localhost/", nil).WithContext(ctx))
	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

func TestProxyHTTPS(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p, _ := testProxy(t)
	ca, err := localca.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(p)
	srv.TLS = &tls.Config{GetCertificate: ca.GetCertificate}
	srv.StartTLS()
	defer srv.Close()

	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	addr := srv.Listener.Addr().String()
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}}
	resp, err := client.Get("https://myapp.localhost/")
	if err != nil {
		t.Fatalf("HTTPS with the local CA: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "hello from the app" {
		t.Errorf("body = %q", body)
	}
}

func TestProxyInfoURL(t *testing.T) {
	p, _ := testProxy(t)
	p.httpsPort = 7443
	info := p.info()
	if got := info.URL("myapp.localhost", false); got != "http://myapp.localhost/" {
		t.Errorf("http URL = %q", got)
	}
	if got := info.URL("myapp.localhost", true); got != "https://myapp.localhost:7443/" {
		t.Errorf("https URL = %q", got)
	}
	if len(info.Routes) != 1 || info.Routes[0].Container != "silo-myapp" {
		t.Errorf("routes = %+v", info.Routes)
	}
}
