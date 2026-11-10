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

// HTTPSocketPath is the Unix socket where Incus forwards a container's http:
// port for the .localhost proxy (a proxy device that `silo up` adds). The
// proxy only connects there, so a route can only reach its own container,
// whatever a project's .silo.yml says about host ports.
func HTTPSocketPath(container string, containerPort int) string {
	return filepath.Join(RuntimeDir(), "http", fmt.Sprintf("%s-%d.sock", container, containerPort))
}
