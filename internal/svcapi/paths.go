package svcapi

import (
	"fmt"
	"os"
	"path/filepath"
)

// RuntimeDir is where the service keeps its sockets: $XDG_RUNTIME_DIR/silo, a
// tmpfs only this user can read, so the sockets disappear on logout and reboot.
func RuntimeDir() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	return filepath.Join(dir, "silo")
}

// SocketPath is the control socket the silo CLI talks to. It's never mounted
// into a container: containers only get their own socket (see hostsock.go).
func SocketPath() string {
	return filepath.Join(RuntimeDir(), "silo.sock")
}
