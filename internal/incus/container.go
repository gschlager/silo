package incus

import (
	"context"
	"fmt"
	"time"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

// Launch creates and starts a new container from the given image.
func Launch(ctx context.Context, server incuscli.InstanceServer, image, name string) error {
	// Connect to the default image server.
	imageServer, err := incuscli.ConnectSimpleStreams("https://images.linuxcontainers.org", nil)
	if err != nil {
		return fmt.Errorf("connecting to image server: %w", err)
	}

	// Find the image alias.
	alias, _, err := imageServer.GetImageAlias(image)
	if err != nil {
		return fmt.Errorf("finding image %q: %w", image, err)
	}

	// Create the instance.
	req := api.InstancesPost{
		Name: name,
		Type: api.InstanceTypeContainer,
		Source: api.InstanceSource{
			Type:     "image",
			Server:   "https://images.linuxcontainers.org",
			Protocol: "simplestreams",
			Alias:    alias.Name,
		},
	}

	op, err := server.CreateInstance(req)
	if err != nil {
		return fmt.Errorf("creating container %q: %w", name, err)
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
			return fmt.Errorf("waiting for container creation: %w", err)
		}
	}

	// Start the container.
	return Start(ctx, server, name)
}

// Start starts a stopped container.
func Start(ctx context.Context, server incuscli.InstanceServer, name string) error {
	op, err := server.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}, "")
	if err != nil {
		return fmt.Errorf("starting container %q: %w", name, err)
	}

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

// Stop stops a running container.
func Stop(ctx context.Context, server incuscli.InstanceServer, name string) error {
	op, err := server.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "stop",
		Timeout: 30,
	}, "")
	if err != nil {
		return fmt.Errorf("stopping container %q: %w", name, err)
	}

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

// Restart restarts a container.
func Restart(ctx context.Context, server incuscli.InstanceServer, name string) error {
	op, err := server.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "restart",
		Timeout: 30,
	}, "")
	if err != nil {
		return fmt.Errorf("restarting container %q: %w", name, err)
	}

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

// Delete removes a container (must be stopped first).
func Delete(ctx context.Context, server incuscli.InstanceServer, name string) error {
	op, err := server.DeleteInstance(name)
	if err != nil {
		return fmt.Errorf("deleting container %q: %w", name, err)
	}

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

// WaitForNetwork polls until DNS resolution works inside the container.
// It respects context cancellation instead of using a fixed deadline.
func WaitForNetwork(ctx context.Context, server incuscli.InstanceServer, name string, hostname string) error {
	for {
		_, err := Exec(ctx, server, name, ExecOpts{}, []string{
			"getent", "hosts", hostname,
		})
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("container %q: network not ready: %w", name, ctx.Err())
		case <-time.After(500 * time.Millisecond):
			// retry
		}
	}
}

// ListSiloInstances returns all Incus instances whose name starts with "silo-".
func ListSiloInstances(server incuscli.InstanceServer) ([]api.Instance, error) {
	instances, err := server.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return nil, fmt.Errorf("listing instances: %w", err)
	}
	var result []api.Instance
	for _, inst := range instances {
		if len(inst.Name) > 5 && inst.Name[:5] == "silo-" {
			result = append(result, inst)
		}
	}
	return result, nil
}

// Exists checks if a container with the given name exists.
func Exists(server incuscli.InstanceServer, name string) bool {
	_, _, err := server.GetInstance(name)
	return err == nil
}

// IsRunning checks if a container is currently running.
func IsRunning(server incuscli.InstanceServer, name string) bool {
	inst, _, err := server.GetInstance(name)
	if err != nil {
		return false
	}
	return inst.Status == "Running"
}

// GetInstance returns the full instance details.
func GetInstance(server incuscli.InstanceServer, name string) (*api.Instance, error) {
	inst, _, err := server.GetInstance(name)
	if err != nil {
		return nil, fmt.Errorf("getting container %q: %w", name, err)
	}
	return inst, nil
}

// GetInstanceState returns the runtime state (CPU, memory, disk, network).
func GetInstanceState(server incuscli.InstanceServer, name string) (*api.InstanceState, error) {
	state, _, err := server.GetInstanceState(name)
	if err != nil {
		return nil, fmt.Errorf("getting state for %q: %w", name, err)
	}
	return state, nil
}

// SnapshotCount returns the number of snapshots for a container.
func SnapshotCount(server incuscli.InstanceServer, name string) int {
	snaps, err := server.GetInstanceSnapshots(name)
	if err != nil {
		return 0
	}
	return len(snaps)
}

// GetVolumeUsage returns the total disk usage (including snapshots) for a container's
// storage volume. Returns -1 if the usage cannot be determined.
func GetVolumeUsage(server incuscli.InstanceServer, name string) (int64, error) {
	// Find which storage pool the container uses.
	inst, _, err := server.GetInstance(name)
	if err != nil {
		return -1, err
	}

	pool := ""
	for _, dev := range inst.ExpandedDevices {
		if dev["type"] == "disk" && dev["path"] == "/" {
			pool = dev["pool"]
			break
		}
	}
	if pool == "" {
		return -1, fmt.Errorf("no root disk found")
	}

	state, err := server.GetStoragePoolVolumeState(pool, "container", name)
	if err != nil {
		return -1, err
	}
	if state == nil || state.Usage == nil {
		return -1, fmt.Errorf("no usage data")
	}

	return int64(state.Usage.Used), nil
}
