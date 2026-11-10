package service

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	"github.com/gschlager/silo/internal/svcapi"
)

// startMarkTTL is how long a "silo starts this one itself" mark is honored.
const startMarkTTL = 5 * time.Minute

// startMarks remembers containers that silo commands are starting themselves.
type startMarks struct {
	mu    sync.Mutex
	marks map[string]time.Time
}

func (m *startMarks) mark(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.marks == nil {
		m.marks = map[string]time.Time{}
	}
	m.marks[name] = time.Now()
}

// take reports whether the container was marked recently, and clears it.
func (m *startMarks) take(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.marks[name]
	delete(m.marks, name)
	return ok && time.Since(t) < startMarkTTL
}

func (s *Server) lifecycleRoutes() {
	handle(s.mux, "POST /v1/containers/starting", func(r *http.Request, in *svcapi.ContainerRef) (none, error) {
		s.starts.mark(in.Name)
		return none{}, nil
	})
}

// onStarted runs when a silo container started. Unless a silo command
// started it and takes care of it, the service does what `silo up` does after
// a start: reconcile the daemon units, inject the environment and secrets
// and start the autostart daemons. Without this, a container lazily started
// by `silo ra` or started with `incus start` had no daemons running.
func (s *Server) onStarted(name string) {
	if s.starts.take(name) {
		return
	}
	srv := s.incus()
	if srv == nil {
		return
	}
	inst, _, err := srv.GetInstance(name)
	if err != nil || inst.Config[incus.TmpMarkerKey] == "true" || inst.Config[incus.BaseMarkerKey] == "true" {
		return
	}
	cfg, err := s.containerConfig(name)
	if err != nil {
		log.Printf("%s: started, but can't load its config: %v", name, err)
		return
	}
	autostart := 0
	for _, d := range cfg.Daemons {
		if d.Autostart {
			autostart++
		}
	}
	if autostart == 0 {
		return
	}
	if !provision.IsInitialized(srv, name, cfg.User) {
		return // still being provisioned by someone
	}

	log.Printf("%s: started outside silo up, starting %d autostart daemons", name, autostart)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	err = provision.ReconcileDaemons(ctx, srv, name, cfg.User, cfg.Shell, cfg.WorkspacePath(), cfg.Daemons)
	if err == nil {
		err = provision.StartConfiguredDaemons(ctx, srv, cfg)
	}
	if err == nil {
		s.events.publish(Event{Type: "daemons-started", Container: name})
	} else {
		s.events.publish(Event{Type: "daemons-failed", Container: name, Message: err.Error()})
		log.Printf("%s: starting daemons: %v", name, err)
		go desktopNotify(context.Background(), "silo: "+projectLabel(name),
			fmt.Sprintf("Couldn't start the daemons: %v. Run silo up in the project.", err))
	}
}
