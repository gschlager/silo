package provision

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/incus"
)

// NotifySocketPath returns the host-side socket path for a project.
func NotifySocketPath(containerName string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("silo-%s-notify.sock", containerName))
}

// SetupNotifications creates the notification socket bridge.
func SetupNotifications(ctx context.Context, server incuscli.InstanceServer, container, username string) error {
	sockPath := NotifySocketPath(container)

	// Remove stale socket if it exists.
	os.Remove(sockPath)

	// Start the host-side listener.
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("creating notification socket: %w", err)
	}

	// Make the socket world-writable so the container can write to it.
	os.Chmod(sockPath, 0700)

	// Run listener in background goroutine.
	go func() {
		defer listener.Close()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // socket closed
			}
			go handleNotification(conn, container)
		}
	}()

	// Mount the socket into the container.
	if err := incus.AddDiskDevice(ctx, server, container, "notify-sock", sockPath, "/run/silo/notify.sock", false); err != nil {
		listener.Close()
		os.Remove(sockPath)
		return fmt.Errorf("mounting notification socket: %w", err)
	}

	// Install the silo-notify helper inside the container.
	helperScript := `#!/bin/sh
echo "$*" | socat - UNIX-CONNECT:/run/silo/notify.sock
`
	rootOpts := incus.ExecOpts{}
	if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
		"sh", "-c", fmt.Sprintf(`cat > /usr/local/bin/silo-notify << 'SCRIPT'
%sSCRIPT
chmod 755 /usr/local/bin/silo-notify`, helperScript),
	}); err != nil {
		return fmt.Errorf("installing silo-notify helper: %w", err)
	}

	return nil
}

// CleanupNotifications removes the notification socket.
func CleanupNotifications(containerName string) {
	os.Remove(NotifySocketPath(containerName))
}

func handleNotification(conn net.Conn, containerName string) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		message := scanner.Text()
		if message != "" {
			sendDesktopNotification(containerName, message)
		}
	}
}

func sendDesktopNotification(containerName, message string) {
	// Try notify-send first, then kdialog.
	if path, err := exec.LookPath("notify-send"); err == nil {
		exec.Command(path, fmt.Sprintf("silo: %s", containerName), message).Run()
		return
	}
	if path, err := exec.LookPath("kdialog"); err == nil {
		exec.Command(path, "--passivepopup", message, "5", "--title", fmt.Sprintf("silo: %s", containerName)).Run()
	}
}
