package service

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gschlager/silo/internal/svcapi"
)

// secretMaxAge is how long a cached secret is kept at most, even while the
// session stays unlocked. It's the longest an op CLI authorization lasts.
const secretMaxAge = 12 * time.Hour

// secretCache keeps values silo read from 1Password, in memory only, so the
// next silo command doesn't need `op` (and maybe a 1Password approval) again.
//
// The cache only answers on the control socket, which only this user can
// open. It's cleared when the screen locks, before suspend, with
// `silo service forget` and when the service stops.
//
// Why this doesn't make things worse: containers can't reach it, and a
// process on the host running as you can already read every secret silo has
// injected into a running container (being in incus-admin is root-equivalent).
type secretCache struct {
	mu      sync.Mutex
	entries map[svcapi.SecretRef]cachedSecret
	now     func() time.Time
}

type cachedSecret struct {
	value string
	added time.Time
}

func newSecretCache() *secretCache {
	return &secretCache{entries: map[svcapi.SecretRef]cachedSecret{}, now: time.Now}
}

func (c *secretCache) get(ref svcapi.SecretRef) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[ref]
	if !ok {
		return "", false
	}
	if c.now().Sub(e.added) > secretMaxAge {
		delete(c.entries, ref)
		return "", false
	}
	return e.value, true
}

func (c *secretCache) put(ref svcapi.SecretRef, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[ref] = cachedSecret{value: value, added: c.now()}
}

// forget drops every cached secret and returns how many there were.
func (c *secretCache) forget(reason string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.entries)
	clear(c.entries)
	if n > 0 {
		log.Printf("forgot %d cached secrets (%s)", n, reason)
	}
	return n
}

// Get and Put let the service's own provisioning code use the cache
// directly (see provision.Secrets).
func (c *secretCache) Get(ref svcapi.SecretRef) (string, bool) { return c.get(ref) }
func (c *secretCache) Put(ref svcapi.SecretRef, value string)  { c.put(ref, value) }

func (c *secretCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (s *Server) secretRoutes() {
	handle(s.mux, "POST /v1/secrets/get", func(r *http.Request, in *svcapi.SecretRef) (svcapi.SecretValue, error) {
		v, ok := s.secrets.get(*in)
		return svcapi.SecretValue{Value: v, Found: ok}, nil
	})
	handle(s.mux, "POST /v1/secrets/put", func(r *http.Request, in *svcapi.SecretPut) (none, error) {
		if in.Ref.Ref == "" {
			return none{}, errStatus(http.StatusBadRequest, "missing ref")
		}
		s.secrets.put(in.Ref, in.Value)
		return none{}, nil
	})
	handle(s.mux, "POST /v1/secrets/forget", func(r *http.Request, _ *none) (svcapi.ForgetResult, error) {
		n := s.secrets.forget("asked to")
		s.events.publish(Event{Type: "secrets-forgotten", Message: "asked to"})
		return svcapi.ForgetResult{Count: n}, nil
	})
}
