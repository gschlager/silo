package incus

import (
	"fmt"
	"strconv"

	incuscli "github.com/lxc/incus/v6/client"
)

// AddDiskDevice adds a bind mount device to the container.
func AddDiskDevice(server incuscli.InstanceServer, container, name, source, path string, readonly bool) error {
	inst, etag, err := server.GetInstance(container)
	if err != nil {
		return fmt.Errorf("getting container %q: %w", container, err)
	}

	if inst.Devices == nil {
		inst.Devices = make(map[string]map[string]string)
	}

	device := map[string]string{
		"type":   "disk",
		"source": source,
		"path":   path,
		"shift":  "true",
	}
	if readonly {
		device["readonly"] = "true"
	}

	inst.Devices[name] = device

	op, err := server.UpdateInstance(container, inst.Writable(), etag)
	if err != nil {
		return fmt.Errorf("adding disk device %q to %q: %w", name, container, err)
	}
	return op.Wait()
}

// AddProxyDevice adds a port forward device to the container.
func AddProxyDevice(server incuscli.InstanceServer, container, name string, hostPort, containerPort int) error {
	inst, etag, err := server.GetInstance(container)
	if err != nil {
		return fmt.Errorf("getting container %q: %w", container, err)
	}

	if inst.Devices == nil {
		inst.Devices = make(map[string]map[string]string)
	}

	inst.Devices[name] = map[string]string{
		"type":    "proxy",
		"listen":  "tcp:0.0.0.0:" + strconv.Itoa(hostPort),
		"connect": "tcp:127.0.0.1:" + strconv.Itoa(containerPort),
		"bind":    "host",
	}

	op, err := server.UpdateInstance(container, inst.Writable(), etag)
	if err != nil {
		return fmt.Errorf("adding proxy device %q to %q: %w", name, container, err)
	}
	return op.Wait()
}

// RemoveDevice removes a device from the container.
func RemoveDevice(server incuscli.InstanceServer, container, name string) error {
	inst, etag, err := server.GetInstance(container)
	if err != nil {
		return fmt.Errorf("getting container %q: %w", container, err)
	}

	delete(inst.Devices, name)

	op, err := server.UpdateInstance(container, inst.Writable(), etag)
	if err != nil {
		return fmt.Errorf("removing device %q from %q: %w", name, container, err)
	}
	return op.Wait()
}

// SetConfig sets an instance configuration key.
func SetConfig(server incuscli.InstanceServer, container, key, value string) error {
	inst, etag, err := server.GetInstance(container)
	if err != nil {
		return fmt.Errorf("getting container %q: %w", container, err)
	}

	if inst.Config == nil {
		inst.Config = make(map[string]string)
	}
	inst.Config[key] = value

	op, err := server.UpdateInstance(container, inst.Writable(), etag)
	if err != nil {
		return fmt.Errorf("setting config %q on %q: %w", key, container, err)
	}
	return op.Wait()
}
