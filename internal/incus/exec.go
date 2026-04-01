package incus

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"golang.org/x/term"
)

// ExecOpts configures command execution inside a container.
type ExecOpts struct {
	User    uint32
	WorkDir string
	Env     map[string]string
}

// Exec runs a command inside the container and returns its combined output.
func Exec(server incuscli.InstanceServer, container string, opts ExecOpts, command []string) (string, error) {
	var stdout, stderr bytes.Buffer

	req := api.InstanceExecPost{
		Command:     command,
		WaitForWS:   true,
		Interactive: false,
		User:        opts.User,
		Cwd:         opts.WorkDir,
		Environment: opts.Env,
	}

	args := &incuscli.InstanceExecArgs{
		Stdin:  io.NopCloser(bytes.NewReader(nil)),
		Stdout: &stdout,
		Stderr: &stderr,
	}

	op, err := server.ExecInstance(container, req, args)
	if err != nil {
		return "", fmt.Errorf("exec in %q: %w", container, err)
	}

	if err := op.Wait(); err != nil {
		return "", fmt.Errorf("exec in %q: %w\nstderr: %s", container, err, stderr.String())
	}

	// Check exit code.
	opAPI := op.Get()
	if opAPI.Metadata != nil {
		if retVal, ok := opAPI.Metadata["return"]; ok {
			if code, ok := retVal.(float64); ok && code != 0 {
				return stdout.String(), fmt.Errorf("command exited with code %d\nstderr: %s", int(code), stderr.String())
			}
		}
	}

	return stdout.String(), nil
}

// ExecInteractive runs a command inside the container with full TTY passthrough.
func ExecInteractive(server incuscli.InstanceServer, container string, opts ExecOpts, command []string) error {
	// Get terminal size.
	width, height := 80, 24
	if term.IsTerminal(int(os.Stdin.Fd())) {
		w, h, err := term.GetSize(int(os.Stdin.Fd()))
		if err == nil {
			width, height = w, h
		}
	}

	req := api.InstanceExecPost{
		Command:     command,
		WaitForWS:   true,
		Interactive: true,
		Width:       width,
		Height:      height,
		User:        opts.User,
		Cwd:         opts.WorkDir,
		Environment: opts.Env,
	}

	// Set terminal to raw mode.
	var oldState *term.State
	if term.IsTerminal(int(os.Stdin.Fd())) {
		var err error
		oldState, err = term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("setting raw terminal mode: %w", err)
		}
		defer term.Restore(int(os.Stdin.Fd()), oldState)
	}

	// Handle window resize signals.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	args := &incuscli.InstanceExecArgs{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	op, err := server.ExecInstance(container, req, args)
	if err != nil {
		return fmt.Errorf("interactive exec in %q: %w", container, err)
	}

	return op.Wait()
}

// ExecStreaming runs a command and streams stdout/stderr to the given writers.
func ExecStreaming(server incuscli.InstanceServer, container string, opts ExecOpts, command []string, stdout, stderr io.Writer) error {
	req := api.InstanceExecPost{
		Command:     command,
		WaitForWS:   true,
		Interactive: false,
		User:        opts.User,
		Cwd:         opts.WorkDir,
		Environment: opts.Env,
	}

	args := &incuscli.InstanceExecArgs{
		Stdin:  io.NopCloser(bytes.NewReader(nil)),
		Stdout: stdout,
		Stderr: stderr,
	}

	op, err := server.ExecInstance(container, req, args)
	if err != nil {
		return fmt.Errorf("exec in %q: %w", container, err)
	}

	if err := op.Wait(); err != nil {
		return fmt.Errorf("exec in %q: %w", container, err)
	}

	opAPI := op.Get()
	if opAPI.Metadata != nil {
		if retVal, ok := opAPI.Metadata["return"]; ok {
			if code, ok := retVal.(float64); ok && code != 0 {
				return fmt.Errorf("command exited with code %d", int(code))
			}
		}
	}

	return nil
}
