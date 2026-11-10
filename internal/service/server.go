// Package service is the silo service: a long-running process per user that
// does what a single silo command can't. It caches 1Password secrets, answers
// containers on their own sockets (notifications, opening URLs, commit
// signing), proxies <project>.localhost to containers, follows Incus events,
// stops idle containers and runs base template refreshes.
//
// It runs as a systemd user service (silo.service), started through
// silo.socket. The CLI talks to it with HTTP over the control socket; every
// feature that uses it falls back to working without it.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gschlager/silo/internal/provision"
	"github.com/gschlager/silo/internal/svcapi"
)

// Server is the running service.
type Server struct {
	opts     Options
	secrets  *secretCache
	started  time.Time
	mux      *http.ServeMux
	stop     chan struct{}
	stopOnce func()
}

// Options turn parts of the service off. Tests use them to run without Incus,
// D-Bus or the proxy ports.
type Options struct {
	NoIncus bool
	NoDBus  bool
	NoProxy bool
}

// Run starts the service and blocks until it's told to stop.
func Run(ctx context.Context) error {
	log.SetFlags(0) // journald adds timestamps
	return runWith(ctx, Options{})
}

func runWith(ctx context.Context, opts Options) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	l, err := listenControl()
	if err != nil {
		return fmt.Errorf("listening on %s: %w", svcapi.SocketPath(), err)
	}

	s := &Server{opts: opts, secrets: newSecretCache(), started: time.Now(), mux: http.NewServeMux(), stop: make(chan struct{})}
	var once sync.Once
	s.stopOnce = func() { once.Do(func() { close(s.stop) }) }
	provision.Secrets = s.secrets
	s.routes()
	s.secretRoutes()
	if !opts.NoDBus {
		go watchSession(ctx, func(reason string) { s.secrets.forget(reason) })
	}

	srv := &http.Server{Handler: s.mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(sameUserListener{l}) }()
	log.Printf("silo service %s listening on %s", svcapi.Version, l.Addr())

	select {
	case <-ctx.Done():
	case <-s.stop:
	case err := <-errc:
		return err
	}
	s.secrets.forget("stopping")
	shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	srv.Shutdown(shutdownCtx)
	log.Printf("silo service stopped")
	return nil
}

// handle registers a JSON endpoint. fn gets the decoded request body (when in
// isn't nil) and returns the response value.
func handle[In any, Out any](mux *http.ServeMux, pattern string, fn func(r *http.Request, in *In) (Out, error)) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		in := new(In)
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(in); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
		}
		out, err := fn(r, in)
		if err != nil {
			status := http.StatusInternalServerError
			var he httpError
			if errors.As(err, &he) {
				status = he.status
			}
			writeError(w, status, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})
}

type httpError struct {
	status int
	msg    string
}

func (e httpError) Error() string { return e.msg }

func errStatus(status int, format string, args ...any) error {
	return httpError{status: status, msg: fmt.Sprintf(format, args...)}
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

type none struct{}

func (s *Server) routes() {
	handle(s.mux, "GET /v1/status", func(r *http.Request, _ *none) (svcapi.StatusInfo, error) {
		return s.status(), nil
	})
	handle(s.mux, "POST /v1/shutdown", func(r *http.Request, _ *none) (none, error) {
		log.Printf("asked to stop")
		// Answer first, then stop.
		time.AfterFunc(50*time.Millisecond, s.stopOnce)
		return none{}, nil
	})
}

func (s *Server) status() svcapi.StatusInfo {
	return svcapi.StatusInfo{
		Version:       svcapi.Version,
		PID:           os.Getpid(),
		Started:       s.started,
		CachedSecrets: s.secrets.len(),
	}
}
