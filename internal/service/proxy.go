package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/localca"
	"github.com/gschlager/silo/internal/svcapi"
)

// The .localhost proxy: http(s)://<project>.localhost goes to the project's
// http: port forward. Browsers resolve every name under .localhost to the
// loopback address themselves, and so does systemd-resolved, so there's
// nothing to set up in DNS.
//
// It connects to the forward's host port, i.e. the Incus proxy device, which
// reaches the container's own localhost. Many dev servers only listen there.
// A request for a stopped container starts it and waits for the port.

const (
	httpPort          = 80
	httpsPort         = 443
	fallbackHTTPPort  = 7080
	fallbackHTTPSPort = 7443
	// upTimeout is how long a request waits for a container to start and its
	// port to accept connections.
	upTimeout = 90 * time.Second
)

type proxyRoute struct {
	container string
	config.HTTPRoute
}

type proxy struct {
	s *Server

	mu        sync.RWMutex
	routes    map[string]proxyRoute
	built     time.Time
	httpPort  int
	httpsPort int

	startMu sync.Mutex
	// starting has a lock per container, so concurrent requests start a
	// container only once.
	starting map[string]*sync.Mutex

	rp *httputil.ReverseProxy
	ui http.Handler
	// dial checks whether a host port accepts connections; a stub in tests.
	dial func(ctx context.Context, addr string) error
}

type targetKey struct{}

func newProxy(s *Server) *proxy {
	p := &proxy{s: s, routes: map[string]proxyRoute{}, starting: map[string]*sync.Mutex{}}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: pr.In.Context().Value(targetKey{}).(string)})
			// Keep the .localhost name: dev servers check the Host header,
			// and Rails, Vite & co. allow .localhost by default.
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
		},
		FlushInterval: -1, // streaming responses, server-sent events, HMR
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			proxyError(w, http.StatusBadGateway, "Couldn't reach the app", err.Error())
		},
	}
	p.dial = func(ctx context.Context, addr string) error {
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return c.Close()
	}
	return p
}

// listen starts the HTTP and HTTPS listeners on loopback. Ports 80 and 443
// need cap_net_bind_service on the silo binary (the RPM and `silo setup` set
// it); without it the proxy uses 7080 and 7443.
func (p *proxy) listen(ctx context.Context) {
	handler := http.Handler(p)
	if hl, port := listenLoopback(httpPort, fallbackHTTPPort); hl != nil {
		srv := &http.Server{Handler: handler, ReadHeaderTimeout: 30 * time.Second, ConnContext: connContext}
		go srv.Serve(hl)
		go func() { <-ctx.Done(); srv.Close() }()
		p.mu.Lock()
		p.httpPort = port
		p.mu.Unlock()
	}

	ca, err := localca.Ensure()
	if err != nil {
		log.Printf("proxy: no HTTPS, can't load the local CA: %v", err)
		return
	}
	if tl, port := listenLoopback(httpsPort, fallbackHTTPSPort); tl != nil {
		srv := &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 30 * time.Second,
			ConnContext:       connContext,
			TLSConfig:         &tls.Config{GetCertificate: ca.GetCertificate, MinVersion: tls.VersionTLS12},
		}
		go srv.ServeTLS(tl, "", "")
		go func() { <-ctx.Done(); srv.Close() }()
		p.mu.Lock()
		p.httpsPort = port
		p.mu.Unlock()
	}
	p.mu.RLock()
	log.Printf("proxy: http on port %d, https on port %d", p.httpPort, p.httpsPort)
	p.mu.RUnlock()
}

// loopbackListener listens on 127.0.0.1 and ::1 (when there is IPv6).
type loopbackListener struct {
	conns  chan net.Conn
	ls     []net.Listener
	closed chan struct{}
	once   sync.Once
}

func (l *loopbackListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *loopbackListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
		for _, x := range l.ls {
			x.Close()
		}
	})
	return nil
}

func (l *loopbackListener) Addr() net.Addr { return l.ls[0].Addr() }

// listenLoopback listens on port, or on fallback when port needs privileges.
func listenLoopback(port, fallback int) (net.Listener, int) {
	for _, p := range []int{port, fallback} {
		l4, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err != nil {
			if errors.Is(err, syscall.EACCES) && p == port {
				log.Printf("proxy: can't use port %d (the silo binary needs cap_net_bind_service, see silo setup), using %d", port, fallback)
				continue
			}
			log.Printf("proxy: can't listen on port %d: %v", p, err)
			if p == port {
				continue
			}
			return nil, 0
		}
		ll := &loopbackListener{conns: make(chan net.Conn), ls: []net.Listener{l4}, closed: make(chan struct{})}
		if l6, err := net.Listen("tcp", net.JoinHostPort("::1", strconv.Itoa(p))); err == nil {
			ll.ls = append(ll.ls, l6)
		}
		for _, x := range ll.ls {
			go func(x net.Listener) {
				for {
					c, err := x.Accept()
					if err != nil {
						return
					}
					select {
					case ll.conns <- c:
					case <-ll.closed:
						c.Close()
						return
					}
				}
			}(x)
		}
		return ll, p
	}
	return nil, 0
}

// rebuild reads the http: forwards of every project container.
func (p *proxy) rebuild() {
	srv := p.s.incus()
	if srv == nil {
		return
	}
	insts, err := incus.ListSiloInstances(srv)
	if err != nil {
		return
	}
	routes := map[string]proxyRoute{}
	for _, inst := range insts {
		if inst.Config[incus.TmpMarkerKey] == "true" {
			continue
		}
		cfg, err := p.s.containerConfig(inst.Name)
		if err != nil {
			continue
		}
		for _, r := range cfg.HTTPRoutes() {
			if prev, ok := routes[r.Host]; ok {
				log.Printf("proxy: %s is claimed by %s and %s, keeping %s", r.Host, prev.container, inst.Name, prev.container)
				continue
			}
			routes[r.Host] = proxyRoute{container: inst.Name, HTTPRoute: r}
		}
	}
	p.mu.Lock()
	p.routes = routes
	p.built = time.Now()
	p.mu.Unlock()
}

// lookup finds the route for a host name, re-reading the configs when the
// name is unknown (a forward added to .silo.yml a moment ago).
func (p *proxy) lookup(host string) (proxyRoute, bool) {
	p.mu.RLock()
	r, ok := p.routes[host]
	fresh := time.Since(p.built) < 5*time.Second
	p.mu.RUnlock()
	if ok || fresh {
		return r, ok
	}
	p.rebuild()
	p.mu.RLock()
	defer p.mu.RUnlock()
	r, ok = p.routes[host]
	return r, ok
}

func (p *proxy) info() svcapi.ProxyInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	info := svcapi.ProxyInfo{HTTPPort: p.httpPort, HTTPSPort: p.httpsPort, Routes: []svcapi.ProxyRoute{}}
	for _, r := range p.routes {
		info.Routes = append(info.Routes, svcapi.ProxyRoute{Host: r.Host, Container: r.container, ContainerPort: r.ContainerPort, HostPort: r.HostPort})
	}
	slices.SortFunc(info.Routes, func(a, b svcapi.ProxyRoute) int { return strings.Compare(a.Host, b.Host) })
	return info
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	if host == UIHost && p.ui != nil {
		p.ui.ServeHTTP(w, r)
		return
	}
	route, ok := p.lookup(host)
	if !ok {
		p.notFound(w, host)
		return
	}
	p.s.activity.touch(route.container)

	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(route.HostPort))
	if err := p.ensureUp(r.Context(), route, target); err != nil {
		proxyError(w, http.StatusBadGateway, "Couldn't reach "+route.Host, err.Error())
		return
	}
	p.rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), targetKey{}, target)))
}

// ensureUp makes sure something accepts connections on the route's host
// port, starting the container when it's stopped.
func (p *proxy) ensureUp(ctx context.Context, route proxyRoute, target string) error {
	quick, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	err := p.dial(quick, target)
	cancel()
	if err == nil {
		return nil
	}

	p.startMu.Lock()
	m, ok := p.starting[route.container]
	if !ok {
		m = &sync.Mutex{}
		p.starting[route.container] = m
	}
	p.startMu.Unlock()
	m.Lock()
	defer m.Unlock()

	wait := 15 * time.Second // a daemon that's starting up
	if srv := p.s.incus(); srv != nil && !incus.IsRunning(srv, route.container) {
		log.Printf("proxy: starting %s for %s", route.container, route.Host)
		if err := incus.Start(ctx, srv, route.container); err != nil {
			return fmt.Errorf("starting %s: %w", route.container, err)
		}
		wait = upTimeout
	}
	deadline := time.Now().Add(wait)
	for {
		try, cancel := context.WithTimeout(ctx, time.Second)
		err := p.dial(try, target)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("nothing accepts connections on port %d in %s (is the daemon running? see silo daemon list)", route.ContainerPort, route.container)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (p *proxy) notFound(w http.ResponseWriter, host string) {
	info := p.info()
	var b strings.Builder
	fmt.Fprintf(&b, `<p>All containers: <a href="%s">%s</a></p>`, html.EscapeString(info.URL(UIHost, false)), UIHost)
	if len(info.Routes) == 0 {
		b.WriteString("<p>No project has an <code>http: true</code> port forward yet.</p>")
	} else {
		b.WriteString("<p>These are the names silo serves:</p><ul>")
		for _, r := range info.Routes {
			u := info.URL(r.Host, false)
			fmt.Fprintf(&b, `<li><a href="%s">%s</a> → %s, port %d</li>`, html.EscapeString(u), html.EscapeString(r.Host), html.EscapeString(r.Container), r.ContainerPort)
		}
		b.WriteString("</ul>")
	}
	proxyPage(w, http.StatusNotFound, "No project is called "+host, b.String())
}

func proxyError(w http.ResponseWriter, status int, title, detail string) {
	proxyPage(w, status, title, "<p>"+html.EscapeString(detail)+"</p>")
}

func proxyPage(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:40rem;margin:4rem auto;padding:0 1rem;color:#222}code{background:#eee;padding:0 .2em}@media(prefers-color-scheme:dark){body{background:#111;color:#ddd}code{background:#333}a{color:#8af}}</style>
<h1>%s</h1>%s<p><small>silo .localhost proxy</small></p>`, html.EscapeString(title), html.EscapeString(title), body)
}
