package provision

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/svcapi"
	incuscli "github.com/lxc/incus/v7/client"
)

// ReconcilePorts makes the container's proxy devices match the configured port
// forwards: it adds a device for every forward and removes any leftover port-*
// devices no longer configured. Proxy devices hot-plug, so this takes effect on
// a running container — a port added on a branch switch (e.g. a new daemon's
// port) becomes reachable after `silo up` without recreating the container.
func ReconcilePorts(ctx context.Context, server incuscli.InstanceServer, container string, ports []config.PortForward) error {
	want, err := addPortDevices(ctx, server, container, ports)
	if err != nil {
		return err
	}
	return pruneOrphanPorts(ctx, server, container, want)
}

// addPortDevices adds an Incus proxy device for each forward and returns the set
// of device names it touched. It rejects two forwards fighting over the same host
// port. AddProxyDevice overwrites an existing device of the same name, so this is
// idempotent.
func addPortDevices(ctx context.Context, server incuscli.InstanceServer, container string, ports []config.PortForward) (map[string]bool, error) {
	want := make(map[string]bool, len(ports))
	seenHost := make(map[int]string)
	for _, port := range ports {
		containerPort, hostPort, err := parsePortSpec(port.Spec)
		if err != nil {
			return nil, fmt.Errorf("invalid port spec %q: %w", port.Spec, err)
		}
		if prev, ok := seenHost[hostPort]; ok {
			return nil, fmt.Errorf("host port %d is forwarded by more than one entry (%s and %s)", hostPort, prev, port.Spec)
		}
		seenHost[hostPort] = port.Spec
		dev := port.DeviceName(hostPort)
		want[dev] = true
		if err := incus.AddProxyDevice(ctx, server, container, dev, hostPort, containerPort); err != nil {
			return nil, err
		}
		if port.HTTP {
			// The .localhost proxy's way in, see svcapi.HTTPSocketPath.
			sock := svcapi.HTTPSocketPath(container, containerPort)
			if err := os.MkdirAll(filepath.Dir(sock), 0700); err != nil {
				return nil, err
			}
			dev := HTTPDeviceName(containerPort)
			want[dev] = true
			if err := incus.AddUnixProxyDevice(ctx, server, container, dev, sock, containerPort); err != nil {
				return nil, err
			}
		}
	}
	return want, nil
}

// HTTPDeviceName is the name of the proxy device that forwards an http:
// port to its Unix socket for the .localhost proxy.
func HTTPDeviceName(containerPort int) string {
	return "http-" + strconv.Itoa(containerPort)
}

// pruneOrphanPorts removes proxy devices named port-* (and http-*) that are no longer wanted,
// so a forward dropped on a branch switch stops listening on the host.
func pruneOrphanPorts(ctx context.Context, server incuscli.InstanceServer, container string, want map[string]bool) error {
	inst, _, err := server.GetInstance(container)
	if err != nil {
		return fmt.Errorf("getting container %q: %w", container, err)
	}
	for name := range inst.Devices {
		if !(strings.HasPrefix(name, "port-") || strings.HasPrefix(name, "http-")) || want[name] {
			continue
		}
		if err := incus.RemoveDevice(ctx, server, container, name); err != nil {
			return err
		}
	}
	return nil
}
