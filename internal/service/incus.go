package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	incuscli "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

// incus returns the current Incus connection, or nil while there is none.
func (s *Server) incus() incuscli.InstanceServer {
	s.incusMu.RLock()
	defer s.incusMu.RUnlock()
	return s.incusSrv
}

func (s *Server) setIncus(srv incuscli.InstanceServer) {
	s.incusMu.Lock()
	s.incusSrv = srv
	s.incusMu.Unlock()
}

// runIncus keeps a connection to Incus and follows its lifecycle events. Incus
// may not be reachable yet (right after `silo setup`, before logging in again
// for the incus-admin group), so it keeps trying.
func (s *Server) runIncus(ctx context.Context) {
	warned := false
	for ctx.Err() == nil {
		srv, err := incus.Connect()
		if err == nil {
			_, _, err = srv.GetServer()
		}
		if err != nil {
			if !warned {
				log.Printf("can't reach Incus yet: %v (retrying every 30s)", err)
				warned = true
			}
			sleepCtx(ctx, 30*time.Second)
			continue
		}
		warned = false
		if err := s.followEvents(ctx, srv); err != nil && ctx.Err() == nil {
			log.Printf("lost the Incus event stream: %v (reconnecting)", err)
			sleepCtx(ctx, 5*time.Second)
		}
	}
}

func (s *Server) followEvents(ctx context.Context, srv incuscli.InstanceServer) error {
	listener, err := srv.GetEvents()
	if err != nil {
		return err
	}
	defer listener.Disconnect()
	if _, err := listener.AddHandler([]string{"lifecycle"}, s.onIncusEvent); err != nil {
		return err
	}

	s.setIncus(srv)
	defer s.setIncus(nil)
	log.Printf("connected to Incus")
	s.hub.reconcile()
	go s.proxy.rebuild()
	go s.refreshOldBases()

	done := make(chan error, 1)
	go func() { done <- listener.Wait() }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-done:
		if err == nil {
			err = fmt.Errorf("event stream closed")
		}
		return err
	}
}

// onIncusEvent handles an Incus lifecycle event for a silo container.
func (s *Server) onIncusEvent(e api.Event) {
	var lc api.EventLifecycle
	if err := json.Unmarshal(e.Metadata, &lc); err != nil {
		return
	}
	name := lc.Name
	if name == "" {
		name = strings.TrimPrefix(lc.Source, "/1.0/instances/")
	}
	if !strings.HasPrefix(lc.Action, "instance-") || !strings.HasPrefix(name, "silo-") || strings.HasPrefix(name, "silo-base-") {
		return
	}
	switch lc.Action {
	case "instance-created", "instance-deleted", "instance-renamed", "instance-updated":
		s.hub.reconcile()
	case "instance-started":
		go s.onStarted(name)
	}
}

// configFor loads the config of a container's project. tmp containers and
// containers without a mounted project directory get the global config only.
func (s *Server) configFor(name string) (*config.MergedConfig, error) {
	srv := s.incus()
	if srv == nil {
		return nil, fmt.Errorf("not connected to Incus")
	}
	inst, _, err := srv.GetInstance(name)
	if err != nil {
		return nil, err
	}
	dir := inst.Devices["workspace"]["source"]
	if inst.Config[incus.TmpMarkerKey] == "true" || dir == "" {
		g, err := config.LoadGlobalConfig()
		if err != nil {
			return nil, err
		}
		home, _ := os.UserHomeDir()
		m := config.Merge(g, nil, home)
		m.ContainerName = name
		return m, nil
	}
	cfg, err := provision.LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	if cfg.ContainerName != name {
		return nil, fmt.Errorf("%s now belongs to %s, not %s", dir, cfg.ContainerName, name)
	}
	return cfg, nil
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
