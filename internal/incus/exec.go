package incus

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
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
	Home    string
	WorkDir string
	Env     map[string]string
}

// UserOpts returns ExecOpts for running commands as a non-root user.
func UserOpts(home, workDir string) ExecOpts {
	return ExecOpts{User: 1000, Home: home, WorkDir: workDir}
}

// resolveEnv returns the environment map, injecting HOME if opts.Home is set and HOME is not already in Env.
func resolveEnv(opts ExecOpts) map[string]string {
	if opts.Home == "" {
		return opts.Env
	}
	env := maps.Clone(opts.Env)
	if env == nil {
		env = make(map[string]string)
	}
	if _, ok := env["HOME"]; !ok {
		env["HOME"] = opts.Home
	}
	return env
}

// Exec runs a command inside the container and returns its combined output.
func Exec(ctx context.Context, server incuscli.InstanceServer, container string, opts ExecOpts, command []string) (string, error) {
	return ExecWithStdin(ctx, server, container, opts, command, nil)
}

// ExecWithStdin runs a command inside the container, piping stdinData to its
// stdin, and returns its combined output. If stdinData is nil, stdin is empty.
func ExecWithStdin(ctx context.Context, server incuscli.InstanceServer, container string, opts ExecOpts, command []string, stdinData []byte) (string, error) {
	var stdout, stderr bytes.Buffer

	var stdin io.ReadCloser
	if stdinData != nil {
		stdin = io.NopCloser(bytes.NewReader(stdinData))
	} else {
		stdin = io.NopCloser(bytes.NewReader(nil))
	}

	req := api.InstanceExecPost{
		Command:     command,
		WaitForWS:   true,
		Interactive: false,
		User:        opts.User,
		Cwd:         opts.WorkDir,
		Environment: resolveEnv(opts),
	}

	args := &incuscli.InstanceExecArgs{
		Stdin:  stdin,
		Stdout: &stdout,
		Stderr: &stderr,
	}

	op, err := server.ExecInstance(container, req, args)
	if err != nil {
		return "", fmt.Errorf("exec in %q: %w", container, err)
	}

	// Wait for the operation, respecting context cancellation.
	errCh := make(chan error, 1)
	go func() { errCh <- op.Wait() }()
	select {
	case <-ctx.Done():
		_ = op.Cancel()
		return "", ctx.Err()
	case err := <-errCh:
		if err != nil {
			return "", fmt.Errorf("exec in %q: %w\nstderr: %s", container, err, stderr.String())
		}
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
func ExecInteractive(ctx context.Context, server incuscli.InstanceServer, container string, opts ExecOpts, command []string) error {
	// Pass TERM from host so the container shell works correctly.
	if opts.Env == nil {
		opts.Env = make(map[string]string)
	}
	if _, ok := opts.Env["TERM"]; !ok {
		if t := os.Getenv("TERM"); t != "" {
			opts.Env["TERM"] = t
		}
	}

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
		Environment: resolveEnv(opts),
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

	// Wait for the operation, respecting context cancellation.
	errCh := make(chan error, 1)
	go func() { errCh <- op.Wait() }()
	select {
	case <-ctx.Done():
		_ = op.Cancel()
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// ExecStreaming runs a command and streams stdout/stderr to the given writers.
func ExecStreaming(ctx context.Context, server incuscli.InstanceServer, container string, opts ExecOpts, command []string, stdout, stderr io.Writer) error {
	req := api.InstanceExecPost{
		Command:     command,
		WaitForWS:   true,
		Interactive: false,
		User:        opts.User,
		Cwd:         opts.WorkDir,
		Environment: resolveEnv(opts),
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

	// Wait for the operation, respecting context cancellation.
	errCh := make(chan error, 1)
	go func() { errCh <- op.Wait() }()
	select {
	case <-ctx.Done():
		_ = op.Cancel()
		return ctx.Err()
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("exec in %q: %w", container, err)
		}
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
