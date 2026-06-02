package color

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	arrow   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))  // green
	warn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))             // yellow
	err_    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))  // red
	dimmed  = lipgloss.NewStyle().Faint(true)
	success = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))  // green
	info_   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))             // cyan
)

var (
	verbose   bool
	startTime time.Time
)

// EnableDebug turns on stage-timing output for Debug calls. Call once early
// in startup so the elapsed clock starts from the same reference.
func EnableDebug() {
	verbose = true
	startTime = time.Now()
}

// captureBuf, when non-nil, collects verbose detail output (command echoes and
// streamed command output) so it can be replayed if a later step fails. It is
// only active in non-verbose mode — in verbose mode detail already streams to
// the console, so there is nothing to replay.
var captureBuf *syncBuffer

// syncBuffer is a bytes.Buffer guarded by a mutex, so a command's stdout and
// stderr streams can be captured into one buffer concurrently without racing.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// StartCapture begins buffering verbose detail output so it can be replayed if
// a later step fails. It is a no-op in verbose mode, where detail already
// streams to the console live. Pair it with DumpCapture (on failure) and
// StopCapture (always, to release the buffer).
func StartCapture() {
	if verbose {
		return
	}
	captureBuf = &syncBuffer{}
}

// StopCapture discards any buffered detail output and stops capturing.
func StopCapture() {
	captureBuf = nil
}

// DumpCapture writes any buffered detail output to stderr under a dim header,
// then stops capturing. Call it when a step fails so the user sees the output
// that was hidden in non-verbose mode. No-op when nothing was captured.
func DumpCapture() {
	if captureBuf == nil {
		return
	}
	out := captureBuf.String()
	captureBuf = nil
	if strings.TrimSpace(out) == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "\n%s\n", dimmed.Render("─── captured output (run with --verbose to stream live) ───"))
	fmt.Fprint(os.Stderr, out)
	if !strings.HasSuffix(out, "\n") {
		fmt.Fprintln(os.Stderr)
	}
}

// DetailOut returns the writer for a command's streamed stdout. In verbose mode
// it is os.Stdout (live); while capturing it is the capture buffer; otherwise
// the output is discarded.
func DetailOut() io.Writer {
	if verbose {
		return os.Stdout
	}
	if captureBuf != nil {
		return captureBuf
	}
	return io.Discard
}

// DetailErr returns the writer for a command's streamed stderr. See DetailOut.
func DetailErr() io.Writer {
	if verbose {
		return os.Stderr
	}
	if captureBuf != nil {
		return captureBuf
	}
	return io.Discard
}

// Debug prints a "[t+0.12s] message" line to stderr, but only when debug
// output is enabled via EnableDebug. Use it to mark stage boundaries in
// commands that go interactive (silo enter, silo ra) — the last printed
// stage tells the user where the session hung.
func Debug(format string, args ...any) {
	if !verbose {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s %s\n",
		dimmed.Render(fmt.Sprintf("[t+%5.2fs]", time.Since(startTime).Seconds())),
		msg)
}

// Status prints a "==> message" line to stderr (green arrow, normal text).
func Status(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s %s\n", arrow.Render("==>"), msg)
}

// Warn prints a yellow "Warning: message" line to stderr.
func Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s %s\n", warn.Render("Warning:"), msg)
}

// Command prints a dimmed "  $ command" line to the detail stream — live in
// verbose mode, captured for error replay otherwise.
func Command(cmd string) {
	fmt.Fprintf(DetailErr(), "  %s\n", dimmed.Render("$ "+cmd))
}

// Success prints a bold green message to stderr.
func Success(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s\n", success.Render(msg))
}

// Info prints an informational message to stderr.
func Info(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// Infof prints a cyan-highlighted message to stderr.
func Infof(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s\n", info_.Render(msg))
}

// Error prints a red error message to stderr.
func Error(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s %s\n", err_.Render("Error:"), msg)
}
