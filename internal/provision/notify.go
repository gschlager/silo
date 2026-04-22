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
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
)

const (
	notifyDeviceName = "notify"
	notifyMountPath  = "/run/silo/notify"
)

// NotifyDir returns the host-side persistent directory bind-mounted into the
// container as /run/silo/notify. The directory always exists so the mount is
// valid even when no silo session is running; the socket file only appears
// inside it while a session listens.
func NotifyDir(containerName string) string {
	return filepath.Join(config.GlobalConfigDir(), "containers", containerName, "notify")
}

// NotifySocketPath returns the host-side socket file path inside NotifyDir.
func NotifySocketPath(containerName string) string {
	return filepath.Join(NotifyDir(containerName), "sock")
}

// SetupNotifications installs the persistent pieces of the notification
// bridge: the host-side directory, the Incus disk device that mounts it into
// the container, and the silo-notify helper script.
func SetupNotifications(ctx context.Context, server incuscli.InstanceServer, container string) error {
	hostDir := NotifyDir(container)
	if err := os.MkdirAll(hostDir, 0700); err != nil {
		return fmt.Errorf("creating notify dir: %w", err)
	}

	if err := incus.AddDiskDevice(ctx, server, container, notifyDeviceName, hostDir, notifyMountPath, false); err != nil {
		return err
	}

	helperScript := fmt.Sprintf(`#!/bin/sh
echo "$*" | socat - UNIX-CONNECT:%s/sock
`, notifyMountPath)

	if _, err := incus.Exec(ctx, server, container, incus.ExecOpts{}, []string{
		"sh", "-c", fmt.Sprintf(`cat > /usr/local/bin/silo-notify << 'SCRIPT'
%sSCRIPT
chmod 755 /usr/local/bin/silo-notify`, helperScript),
	}); err != nil {
		return fmt.Errorf("installing silo-notify helper: %w", err)
	}
	return nil
}

// StartNotifyBridge starts the host-side listener that turns notifications
// sent by silo-notify inside the container into desktop notifications. The
// returned cleanup func stops the listener and removes the socket; callers
// MUST defer it.
func StartNotifyBridge(container string) (func(), error) {
	sockPath := NotifySocketPath(container)
	// A stale socket from a crashed previous session would make Listen fail
	// with "address already in use".
	os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("creating notify socket: %w", err)
	}
	os.Chmod(sockPath, 0700)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleNotification(conn, container)
		}
	}()

	return func() {
		listener.Close()
		os.Remove(sockPath)
	}, nil
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
