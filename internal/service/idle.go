package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	incuscli "github.com/lxc/incus/v7/client"
)

// Idle stop: a running container nobody uses is stopped after idle_stop (4h
// by default), so it doesn't hold memory under the shared cap. It starts
// again lazily on `silo ra`/`silo enter`, or through the .localhost proxy.
//
// A container counts as used while any of these is true:
//   - it has a terminal: any open pty (silo ra/enter sessions, herdr's
//     shells and agents, tmux)
//   - it uses more than idleCPUShare of one CPU core on average since the
//     last check (a build, a test run, a busy daemon)
//   - the proxy served a request for it

const (
	idleCheckInterval = 5 * time.Minute
	idleCPUShare      = 0.05
)

type idleTracker struct {
	mu         sync.Mutex
	lastActive map[string]time.Time
	lastCPU    map[string]cpuSample
}

type cpuSample struct {
	usage int64 // CPU time in nanoseconds
	at    time.Time
}

func newIdleTracker() *idleTracker {
	return &idleTracker{lastActive: map[string]time.Time{}, lastCPU: map[string]cpuSample{}}
}

// started records a container start as activity.
func (t *idleTracker) started(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastActive[name] = time.Now()
	delete(t.lastCPU, name)
}

// cpuBusy records a CPU sample and reports whether the container was busy
// since the last one.
func (t *idleTracker) cpuBusy(name string, usage int64, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, ok := t.lastCPU[name]
	t.lastCPU[name] = cpuSample{usage: usage, at: now}
	if !ok || usage < prev.usage {
		return false
	}
	elapsed := now.Sub(prev.at)
	if elapsed <= 0 {
		return false
	}
	return float64(usage-prev.usage)/float64(elapsed) > idleCPUShare
}

// idleSince returns since when the container has been idle, given whether
// it's active right now. A container seen for the first time counts as
// active now, so a service restart doesn't stop everything.
func (t *idleTracker) idleSince(name string, active bool, now time.Time) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	last, ok := t.lastActive[name]
	if active || !ok {
		t.lastActive[name] = now
		return now
	}
	return last
}

// lastActiveAt returns when the container was last seen in use.
func (t *idleTracker) lastActiveAt(name string) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastActive[name]
}

func (t *idleTracker) forget(keep map[string]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for name := range t.lastActive {
		if !keep[name] {
			delete(t.lastActive, name)
			delete(t.lastCPU, name)
		}
	}
}

// checkIdle stops containers that have been idle for longer than idle_stop.
func (s *Server) checkIdle() {
	srv := s.incus()
	if srv == nil {
		return
	}
	g, err := config.LoadGlobalConfig()
	if err != nil {
		return
	}
	limit := g.IdleStopAfter()
	insts, err := incus.ListSiloInstances(srv)
	if err != nil {
		return
	}
	now := time.Now()
	running := map[string]bool{}
	for _, inst := range insts {
		if inst.Status != "Running" {
			continue
		}
		name := inst.Name
		running[name] = true

		active := now.Sub(s.activity.lastUsed(name)) < idleCheckInterval
		if !active {
			if st, err := incus.GetInstanceState(srv, name); err == nil {
				active = s.idle.cpuBusy(name, st.CPU.Usage, now)
			}
		}
		if !active {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			active = hasTerminal(ctx, srv, name)
			cancel()
		}
		since := s.idle.idleSince(name, active, now)
		if limit == 0 || now.Sub(since) < limit {
			continue
		}

		log.Printf("%s: idle since %s, stopping it", name, since.Format(time.Kitchen))
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := incus.Stop(ctx, srv, name)
		cancel()
		if err != nil {
			log.Printf("%s: stopping: %v", name, err)
			continue
		}
		s.events.publish(Event{Type: "container-idle-stopped", Container: name})
		go desktopNotify(context.Background(), "silo: "+projectLabel(name),
			fmt.Sprintf("Stopped after %s without use. silo ra, silo enter or its .localhost URL starts it again.", formatIdle(limit)))
	}
	s.idle.forget(running)
}

// hasTerminal reports whether any pty is open in the container. When it
// can't tell, it says yes: better keep a container than stop one in use.
func hasTerminal(ctx context.Context, srv incuscli.InstanceServer, name string) bool {
	out, err := incus.Exec(ctx, srv, name, incus.ExecOpts{}, []string{"sh", "-c", "ls /dev/pts"})
	if err != nil {
		return true
	}
	for _, f := range strings.Fields(out) {
		if f != "ptmx" {
			return true
		}
	}
	return false
}

func formatIdle(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return d.Round(time.Minute).String()
}
