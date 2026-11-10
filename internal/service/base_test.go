package service

import (
	"context"
	"testing"
	"time"

	"github.com/gschlager/silo/internal/svcapi"
)

func TestBaseRefreshOnePerImage(t *testing.T) {
	b := newBaseJobs()
	release := make(chan struct{})
	started := make(chan string, 4)
	b.start = func(image string) error {
		started <- image
		<-release
		return nil
	}
	if !b.refresh("fedora/44") {
		t.Fatal("first refresh didn't start")
	}
	<-started
	if b.refresh("fedora/44") {
		t.Error("a second refresh for the same image started")
	}
	if !b.refresh("debian/13") {
		t.Error("a refresh for another image didn't start")
	}
	<-started
	if got := b.images(); len(got) != 2 {
		t.Errorf("running = %v", got)
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	if got := b.images(); len(got) != 0 {
		t.Errorf("still running after finishing: %v", got)
	}
}

func TestBaseRefreshEndpoint(t *testing.T) {
	c := startTestServer(t)
	err := c.Do(context.Background(), "POST", "/v1/base/refresh", svcapi.BaseRefresh{}, nil)
	if err == nil {
		t.Error("a refresh without an image was accepted")
	}
}
