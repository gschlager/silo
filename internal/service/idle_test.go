package service

import (
	"testing"
	"time"
)

func TestIdleTracker(t *testing.T) {
	tr := newIdleTracker()
	t0 := time.Unix(10000, 0)

	// First sight counts as active, so a service restart stops nothing.
	if since := tr.idleSince("silo-a", false, t0); !since.Equal(t0) {
		t.Errorf("first sight: idle since %v", since)
	}
	t1 := t0.Add(time.Hour)
	if since := tr.idleSince("silo-a", false, t1); !since.Equal(t0) {
		t.Errorf("idle since = %v, want %v", since, t0)
	}
	t2 := t1.Add(time.Hour)
	if since := tr.idleSince("silo-a", true, t2); !since.Equal(t2) {
		t.Errorf("active resets: idle since = %v", since)
	}

	tr.forget(map[string]bool{})
	if _, ok := tr.lastActive["silo-a"]; ok {
		t.Error("forget kept a container that's gone")
	}
}

func TestCPUBusy(t *testing.T) {
	tr := newIdleTracker()
	t0 := time.Unix(10000, 0)
	if tr.cpuBusy("silo-a", 0, t0) {
		t.Error("the first sample can't tell")
	}
	// 1% of a core over 5 minutes.
	t1 := t0.Add(5 * time.Minute)
	if tr.cpuBusy("silo-a", int64(float64(5*time.Minute)*0.01), t1) {
		t.Error("1% of a core counted as busy")
	}
	// Half a core over the next 5 minutes.
	t2 := t1.Add(5 * time.Minute)
	if !tr.cpuBusy("silo-a", int64(float64(5*time.Minute)*0.01)+int64(float64(5*time.Minute)*0.5), t2) {
		t.Error("half a core didn't count as busy")
	}
	// A restart resets the counter.
	if tr.cpuBusy("silo-a", 10, t2.Add(time.Minute)) {
		t.Error("a lower counter (restart) counted as busy")
	}
}
