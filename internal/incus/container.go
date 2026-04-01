package incus

import (
	"fmt"
	"time"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

// Launch creates and starts a new container from the given image.
func Launch(server incuscli.InstanceServer, image, name string) error {
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
	if err := op.Wait(); err != nil {
		return fmt.Errorf("waiting for container creation: %w", err)
	}

	// Start the container.
	return Start(server, name)
}

// Start starts a stopped container.
func Start(server incuscli.InstanceServer, name string) error {
	op, err := server.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}, "")
	if err != nil {
		return fmt.Errorf("starting container %q: %w", name, err)
	}
	return op.Wait()
}

// Stop stops a running container.
func Stop(server incuscli.InstanceServer, name string) error {
	op, err := server.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "stop",
		Timeout: 30,
	}, "")
	if err != nil {
		return fmt.Errorf("stopping container %q: %w", name, err)
	}
	return op.Wait()
}

// Restart restarts a container.
func Restart(server incuscli.InstanceServer, name string) error {
	op, err := server.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "restart",
		Timeout: 30,
	}, "")
	if err != nil {
		return fmt.Errorf("restarting container %q: %w", name, err)
	}
	return op.Wait()
}

// Delete removes a container (must be stopped first).
func Delete(server incuscli.InstanceServer, name string) error {
	op, err := server.DeleteInstance(name)
	if err != nil {
		return fmt.Errorf("deleting container %q: %w", name, err)
	}
	return op.Wait()
}

// WaitForNetwork polls until DNS resolution works inside the container.
func WaitForNetwork(server incuscli.InstanceServer, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := Exec(server, name, ExecOpts{}, []string{
			"getent", "hosts", "mirrors.fedoraproject.org",
		})
		if err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("container %q: network not ready after %s", name, timeout)
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
