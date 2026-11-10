package service

import (
	"context"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
)

// hub serves every container's own sockets in its host directory (see
// provision.HostDir). The container is known by the socket a request comes
// in on, so nothing a container sends can claim to be another container.
type hub struct {
	s    *Server
	mu   sync.Mutex
	open map[string]*containerSockets
}

type containerSockets struct {
	closers []io.Closer
}

func (c *containerSockets) close() {
	for _, cl := range c.closers {
		cl.Close()
	}
}

func newHub(s *Server) *hub {
	return &hub{s: s, open: map[string]*containerSockets{}}
}

// reconcile opens the sockets of every silo container that has the host
// device and closes those of containers that are gone.
func (h *hub) reconcile() {
	srv := h.s.incus()
	if srv == nil {
		return
	}
	insts, err := incus.ListSiloInstances(srv)
	if err != nil {
		log.Printf("listing containers: %v", err)
		return
	}
	want := map[string]bool{}
	for _, inst := range insts {
		if _, ok := inst.Devices["host"]; ok {
			want[inst.Name] = true
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	for name, socks := range h.open {
		if !want[name] {
			socks.close()
			delete(h.open, name)
			log.Printf("%s: closed its sockets", name)
		}
	}
	for name := range want {
		if _, ok := h.open[name]; ok {
			continue
		}
		socks, err := h.s.openContainerSockets(name)
		if err != nil {
			log.Printf("%s: can't open its sockets: %v", name, err)
			continue
		}
		h.open[name] = socks
	}
}

func (h *hub) names() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := slices.Sorted(maps.Keys(h.open))
	if names == nil {
		names = []string{}
	}
	return names
}

func (h *hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, socks := range h.open {
		socks.close()
		delete(h.open, name)
	}
}

// openContainerSockets listens on the container's sockets.
func (s *Server) openContainerSockets(name string) (*containerSockets, error) {
	if err := os.MkdirAll(provision.HostDir(name), 0700); err != nil {
		return nil, err
	}
	socks := &containerSockets{}
	l, err := listenUnix(provision.HostAPISocket(name), 0600)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{
		Handler:           s.containerAPI(name),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
	}
	go srv.Serve(l)
	socks.closers = append(socks.closers, srv)

	al, err := listenUnix(provision.HostAgentSocket(name), 0600)
	if err != nil {
		socks.close()
		return nil, err
	}
	go serveSigningAgent(al, &signingAgent{
		container:  name,
		signingKey: configuredSigningKey,
		upstream:   dialHostAgent,
		report: func(typ, msg string) {
			s.events.publish(Event{Type: typ, Container: name, Message: msg})
		},
	})
	socks.closers = append(socks.closers, al)
	return socks, nil
}

// containerAPI is what a container can ask the service for. Everything here
// must be safe for an agent that does whatever it wants inside its container.
func (s *Server) containerAPI(name string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/notify", func(w http.ResponseWriter, r *http.Request) {
		msg, err := readText(w, r, 4096)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.notify(name, msg)
	})
	mux.HandleFunc("POST /v1/open", func(w http.ResponseWriter, r *http.Request) {
		s.handleOpen(name, w, r)
	})
	return mux
}

// readText reads a short text body.
func readText(w http.ResponseWriter, r *http.Request, max int64) (string, error) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// notify shows a desktop notification for a container, when notifications are
// on and herdr isn't alerting for it already.
func (s *Server) notify(container, msg string) {
	if msg == "" {
		return
	}
	if s.events != nil {
		s.events.publish(Event{Type: "notification", Container: container, Message: msg})
	}
	g, err := config.LoadGlobalConfig()
	if err != nil || !g.Notifications {
		return
	}
	if g.Herdr == nil || *g.Herdr {
		if _, ok := provision.FindHerdrHC(); ok {
			return
		}
	}
	go desktopNotify(context.Background(), "silo: "+projectLabel(container), msg)
}

// projectLabel is the container name without the "silo-" prefix.
func projectLabel(container string) string {
	return strings.TrimPrefix(container, "silo-")
}
