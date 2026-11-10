package service

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gschlager/silo/internal/svcapi"
)

// Event is something that happened, as the event stream reports it.
type Event = svcapi.Event

// eventBus fans events out to the subscribers of GET /v1/events. A
// subscriber that doesn't keep up loses events instead of blocking the
// service.
type eventBus struct {
	mu   sync.Mutex
	subs map[chan Event]bool
}

func newEventBus() *eventBus {
	return &eventBus{subs: map[chan Event]bool{}}
}

func (b *eventBus) publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (b *eventBus) subscribe() (chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = true
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// handleEvents streams events as JSON lines until the client goes away.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	ch, cancel := s.events.subscribe()
	defer cancel()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	enc := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stop:
			return
		case e := <-ch:
			if err := enc.Encode(e); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
