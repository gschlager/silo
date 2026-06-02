package color

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestDetailWritersWithoutCapture(t *testing.T) {
	resetCapture(t)

	if got := DetailOut(); got != io.Discard {
		t.Errorf("DetailOut() = %v, want io.Discard when not capturing", got)
	}
	if got := DetailErr(); got != io.Discard {
		t.Errorf("DetailErr() = %v, want io.Discard when not capturing", got)
	}
}

func TestCaptureBuffersDetailOutput(t *testing.T) {
	resetCapture(t)

	StartCapture()

	// stdout and stderr of a command share one buffer so ordering is preserved.
	io.WriteString(DetailOut(), "out line\n")
	io.WriteString(DetailErr(), "err line\n")
	Command("make build")

	got := captureBuf.String()
	for _, want := range []string{"out line", "err line", "$ make build"} {
		if !strings.Contains(got, want) {
			t.Errorf("captured output %q missing %q", got, want)
		}
	}
}

func TestStopCaptureDiscards(t *testing.T) {
	resetCapture(t)

	StartCapture()
	io.WriteString(DetailOut(), "ignored\n")
	StopCapture()

	if captureBuf != nil {
		t.Fatal("StopCapture should clear the buffer")
	}
	if got := DetailOut(); got != io.Discard {
		t.Errorf("DetailOut() = %v after StopCapture, want io.Discard", got)
	}
}

func TestDumpCaptureClearsBuffer(t *testing.T) {
	resetCapture(t)

	StartCapture()
	io.WriteString(DetailOut(), "boom\n")
	DumpCapture()

	if captureBuf != nil {
		t.Error("DumpCapture should clear the buffer after dumping")
	}
}

func TestCaptureNoOpInVerboseMode(t *testing.T) {
	resetCapture(t)
	verbose = true
	t.Cleanup(func() { verbose = false })

	StartCapture()
	if captureBuf != nil {
		t.Fatal("StartCapture should be a no-op in verbose mode")
	}
	if got := DetailOut(); got != os.Stdout {
		t.Errorf("DetailOut() = %v in verbose mode, want os.Stdout", got)
	}
	if got := DetailErr(); got != os.Stderr {
		t.Errorf("DetailErr() = %v in verbose mode, want os.Stderr", got)
	}
}

func TestSyncBufferConcurrentWrites(t *testing.T) {
	resetCapture(t)
	StartCapture()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); io.WriteString(DetailOut(), "o") }()
		go func() { defer wg.Done(); io.WriteString(DetailErr(), "e") }()
	}
	wg.Wait()

	if got := len(captureBuf.String()); got != 100 {
		t.Errorf("captured %d bytes, want 100", got)
	}
}

// resetCapture ensures each test starts from a clean, non-verbose state.
func resetCapture(t *testing.T) {
	t.Helper()
	captureBuf = nil
	verbose = false
}
