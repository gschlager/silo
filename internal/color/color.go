package color

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
)

var (
	arrow   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))  // green
	heading = lipgloss.NewStyle().Bold(true)
	warn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))             // yellow
	err_    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))  // red
	dimmed  = lipgloss.NewStyle().Faint(true)
	success = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))  // green
	info_   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))             // cyan
)

// Status prints a green "==> message" line to stderr.
func Status(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s %s\n", arrow.Render("==>"), heading.Render(msg))
}

// Warn prints a yellow "Warning: message" line to stderr.
func Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s %s\n", warn.Render("Warning:"), msg)
}

// Command prints a dimmed "  $ command" line to stderr.
func Command(cmd string) {
	fmt.Fprintf(os.Stderr, "  %s\n", dimmed.Render("$ "+cmd))
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
