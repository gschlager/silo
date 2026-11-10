package service

import (
	"bufio"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gschlager/silo/internal/svcapi"
)

func TestEventStream(t *testing.T) {
	c := startTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body, err := c.Stream(ctx, "/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()

	// Forgetting secrets publishes an event.
	go func() {
		time.Sleep(100 * time.Millisecond)
		c.Do(context.Background(), "POST", "/v1/secrets/forget", nil, nil)
	}()

	lines := bufio.NewScanner(body)
	if !lines.Scan() {
		t.Fatalf("no event: %v", lines.Err())
	}
	var e svcapi.Event
	if err := json.Unmarshal(lines.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if e.Type != "secrets-forgotten" || e.Time.IsZero() {
		t.Errorf("event = %+v", e)
	}
}

func TestEventBusDropsForSlowSubscribers(t *testing.T) {
	b := newEventBus()
	ch, cancel := b.subscribe()
	defer cancel()
	for i := 0; i < 200; i++ {
		b.publish(Event{Type: "x"}) // must not block
	}
	if len(ch) != cap(ch) {
		t.Errorf("buffered %d of %d", len(ch), cap(ch))
	}
}
