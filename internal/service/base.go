package service

import (
	"log"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gschlager/silo/internal/provision"
	"github.com/gschlager/silo/internal/svcapi"
)

// baseJobs runs base template refreshes, one per image at a time. They run as
// `silo base refresh` child processes (which also take the build lock, so a
// refresh never races a `silo up` that builds the same template), with their
// output in the refresh log.
type baseJobs struct {
	mu      sync.Mutex
	running map[string]bool
	// start runs a refresh and waits for it; a stub in tests.
	start func(image string) error
}

func newBaseJobs() *baseJobs {
	return &baseJobs{
		running: map[string]bool{},
		start:   func(image string) error { return provision.StartRefreshProcess(image, true) },
	}
}

// refresh starts a refresh for the image unless one is running. It reports
// whether it started one.
func (b *baseJobs) refresh(image string) bool {
	b.mu.Lock()
	if b.running[image] {
		b.mu.Unlock()
		return false
	}
	b.running[image] = true
	b.mu.Unlock()

	go func() {
		defer func() {
			b.mu.Lock()
			delete(b.running, image)
			b.mu.Unlock()
		}()
		log.Printf("refreshing the base template for %s (log: %s)", image, provision.RefreshLogPath())
		start := time.Now()
		if err := b.start(image); err != nil {
			log.Printf("base template refresh for %s failed: %v (see %s)", image, err, provision.RefreshLogPath())
			return
		}
		log.Printf("base template refresh for %s done in %s", image, time.Since(start).Round(time.Second))
	}()
	return true
}

func (b *baseJobs) images() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for img := range b.running {
		out = append(out, img)
	}
	slices.Sort(out)
	return out
}

func (s *Server) baseRoutes() {
	handle(s.mux, "POST /v1/base/refresh", func(r *http.Request, in *svcapi.BaseRefresh) (none, error) {
		if in.Image == "" {
			return none{}, errStatus(http.StatusBadRequest, "missing image")
		}
		s.base.refresh(in.Image)
		return none{}, nil
	})
}

// refreshOldBases refreshes every base template older than a week, so the
// next container starts from current packages and agents even when no
// `silo up` noticed the age yet.
func (s *Server) refreshOldBases() {
	srv := s.incus()
	if srv == nil {
		return
	}
	bases, err := provision.ListBases(srv)
	if err != nil {
		log.Printf("listing base templates: %v", err)
		return
	}
	for _, b := range bases {
		if b.Image == "" || b.BuiltAt.IsZero() || time.Since(b.BuiltAt) < provision.BaseMaxAge {
			continue
		}
		if provision.RefreshRecentlyFailed(b.Name) {
			continue
		}
		s.base.refresh(b.Image)
	}
}
