package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gschlager/silo/internal/config"
)

// Opening a URL from a container in the host browser always takes a click on
// a notification. Without that, an agent could open whatever it likes in a
// browser where you're logged in everywhere.

const (
	maxURLLength   = 4096
	confirmTimeout = 2 * time.Minute
)

// openPending tracks containers with an open request waiting for a click, so a
// container can't pile up notifications.
type openPending struct {
	mu      sync.Mutex
	waiting map[string]bool
}

func (p *openPending) start(container string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiting == nil {
		p.waiting = map[string]bool{}
	}
	if p.waiting[container] {
		return false
	}
	p.waiting[container] = true
	return true
}

func (p *openPending) done(container string) {
	p.mu.Lock()
	delete(p.waiting, container)
	p.mu.Unlock()
}

// handleOpen asks to open a URL from a container. It answers right away; the
// URL opens once the notification is clicked.
func (s *Server) handleOpen(container string, w http.ResponseWriter, r *http.Request) {
	raw, err := readText(w, r, maxURLLength)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg, err := s.containerConfig(container)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target, err := hostURL(raw, cfg.Ports, s.proxyURLFor(cfg))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.opening.start(container) {
		http.Error(w, "another URL from this container is still waiting for a click", http.StatusTooManyRequests)
		return
	}
	go func() {
		defer s.opening.done(container)
		ctx, cancel := context.WithTimeout(context.Background(), confirmTimeout)
		defer cancel()
		title := "silo: " + projectLabel(container) + " wants to open"
		if !confirmOpen(ctx, title, target) {
			log.Printf("%s: not opening %s (no click)", container, target)
			return
		}
		log.Printf("%s: opening %s", container, target)
		openInBrowser(target)
	}()
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "Click the notification on the host to open %s\n", target)
}

// hostURL checks a URL from a container and turns it into one that works on
// the host. Only http and https are allowed. A URL for the container's own
// localhost is rewritten to where that port is forwarded on the host
// (through the .localhost proxy for http: forwards, when it runs).
func hostURL(raw string, ports []config.PortForward, proxyURL func(containerPort int) string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("not a URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("only http and https URLs can be opened")
	}
	if u.Host == "" || u.User != nil {
		return "", errors.New("the URL needs a host and no user info")
	}
	if !isLoopbackHost(u.Hostname()) {
		return u.String(), nil
	}

	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	cport, _ := strconv.Atoi(port)
	if proxyURL != nil {
		if base := proxyURL(cport); base != "" {
			pu, _ := url.Parse(base)
			u.Scheme, u.Host = pu.Scheme, pu.Host
			return u.String(), nil
		}
	}
	for _, p := range ports {
		cp, ok1 := p.ContainerPort()
		hp, ok2 := p.HostPort()
		if ok1 && ok2 && cp == cport {
			u.Host = net.JoinHostPort("localhost", strconv.Itoa(hp))
			return u.String(), nil
		}
	}
	return "", fmt.Errorf("port %d of the container isn't forwarded to the host (add it to ports: in .silo.yml)", cport)
}

// isLoopbackHost reports whether h means the container itself. Names under
// .localhost are left alone: on the host they reach the .localhost proxy.
// proxyURLFor returns a function that gives the .localhost URL for a port of
// the container, or "" when the proxy doesn't serve it.
func (s *Server) proxyURLFor(cfg *config.MergedConfig) func(int) string {
	return func(containerPort int) string {
		if s.proxy == nil {
			return ""
		}
		info := s.proxy.info()
		for _, r := range cfg.HTTPRoutes() {
			if r.ContainerPort == containerPort {
				return info.URL(r.Host, false)
			}
		}
		return ""
	}
}

func isLoopbackHost(h string) bool {
	if h == "localhost" || h == "0.0.0.0" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// confirmOpen shows a notification with an "Open" button and reports whether
// it was clicked. A variable, so tests can click.
var confirmOpen = func(ctx context.Context, title, target string) bool {
	path, err := exec.LookPath("notify-send")
	if err != nil {
		log.Printf("can't ask before opening a URL: notify-send isn't installed")
		return false
	}
	out, err := exec.CommandContext(ctx, path,
		"--app-name=silo", "--action=open=Open", "--wait",
		"--expire-time="+strconv.Itoa(int(confirmTimeout/time.Millisecond)),
		title, target).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "open"
}

// openInBrowser opens a URL with the host's default browser. A variable, so
// tests don't open browsers.
var openInBrowser = func(target string) {
	cmd := exec.Command("xdg-open", target)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		log.Printf("xdg-open: %v", err)
		return
	}
	go cmd.Wait()
}
