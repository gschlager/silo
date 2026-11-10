package service

import (
	"sync"
	"time"
)

// activity remembers when a container was last used through the service
// (proxy requests), so idle stop doesn't stop a container someone is
// browsing.
type activity struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (a *activity) touch(container string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		a.last = map[string]time.Time{}
	}
	a.last[container] = time.Now()
}

// lastUsed returns when the container was last used, or the zero time.
func (a *activity) lastUsed(container string) time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last[container]
}
