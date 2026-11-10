package service

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gschlager/silo/internal/svcapi"
	"golang.org/x/sys/unix"
)

// listenControl returns the control socket listener: the one systemd passed
// when the service is socket-activated, otherwise a socket it creates itself
// (for running `silo service run` by hand).
func listenControl() (net.Listener, error) {
	if l, err := activationListener(); l != nil || err != nil {
		return l, err
	}
	return listenUnix(svcapi.SocketPath(), 0600)
}

// activationListener returns the first socket passed by systemd socket
// activation, or nil if there is none. See sd_listen_fds(3).
func activationListener() (net.Listener, error) {
	if os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid()) {
		return nil, nil
	}
	n, _ := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	os.Unsetenv("LISTEN_PID")
	os.Unsetenv("LISTEN_FDS")
	os.Unsetenv("LISTEN_FDNAMES")
	if n < 1 {
		return nil, nil
	}
	const firstFD = 3
	unix.CloseOnExec(firstFD)
	f := os.NewFile(firstFD, "silo.sock")
	l, err := net.FileListener(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("using the socket from systemd: %w", err)
	}
	return l, nil
}

// listenUnix creates a Unix socket at path with the given mode, in a directory
// only this user can enter. A stale socket from a crashed run is replaced.
func listenUnix(path string, mode os.FileMode) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	old := unix.Umask(0177)
	l, err := net.Listen("unix", path)
	unix.Umask(old)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, mode); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// sameUserListener only accepts connections from processes running as this
// user. The socket's file mode already says that; checking the peer's
// credentials as well means a socket that ended up with the wrong mode, or a
// file descriptor passed to another user, can't be used to reach the service.
type sameUserListener struct {
	net.Listener
}

func (l sameUserListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(c)
		if err == nil && uid == os.Getuid() {
			return c, nil
		}
		c.Close()
	}
}

func peerUID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1, errors.New("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if credErr != nil {
		return -1, credErr
	}
	return int(cred.Uid), nil
}
