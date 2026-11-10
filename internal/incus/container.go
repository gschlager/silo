package incus

import (
	"context"
	"fmt"
	"strings"
	"time"

	incuscli "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"

	"github.com/gschlager/silo/internal/color"
)

const imageServerURL = "https://images.linuxcontainers.org"

// Launch creates and starts a new container from the given image.
func Launch(ctx context.Context, server incuscli.InstanceServer, image, name string) error {
	if err := Create(ctx, server, image, name); err != nil {
		return err
	}
	return Start(ctx, server, name)
}

// Create creates a new container from the given image without starting it.
func Create(ctx context.Context, server incuscli.InstanceServer, image, name string) error {
	source, err := imageSource(server, image)
	if err != nil {
		return err
	}
	return createInstance(ctx, server, name, source)
}

// CopySnapshot creates a new, stopped container from a snapshot of another
// one. The server drops the source's volatile keys (MAC addresses etc.), so
// the copy gets its own. On btrfs and ZFS pools this is a cheap clone.
func CopySnapshot(ctx context.Context, server incuscli.InstanceServer, source, snapshot, name string) error {
	return createInstance(ctx, server, name, api.InstanceSource{
		Type:   "copy",
		Source: source + "/" + snapshot,
	})
}

func createInstance(ctx context.Context, server incuscli.InstanceServer, name string, source api.InstanceSource) error {
	op, err := server.CreateInstance(api.InstancesPost{
		Name:   name,
		Type:   api.InstanceTypeContainer,
		Source: source,
	})
	if err != nil {
		return fmt.Errorf("creating container %q: %w", name, err)
	}
	if err := waitOp(ctx, op); err != nil {
		return fmt.Errorf("waiting for container creation: %w", err)
	}
	return nil
}

// Rename renames a stopped container.
func Rename(ctx context.Context, server incuscli.InstanceServer, name, newName string) error {
	op, err := server.RenameInstance(name, api.InstancePost{Name: newName})
	if err != nil {
		return fmt.Errorf("renaming container %q to %q: %w", name, newName, err)
	}
	return waitOp(ctx, op)
}

// waitOp waits for an Incus operation, respecting context cancellation.
func waitOp(ctx context.Context, op incuscli.Operation) error {
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

// imageSource resolves image to an instance source. It prefers the public
// image server so a fresh container tracks the latest build, but falls back to
// a copy already cached locally when the server is unreachable or no longer
// publishes that alias — which is what happens when an upstream image build
// breaks (e.g. the Fedora images vanishing from images.linuxcontainers.org).
func imageSource(server incuscli.InstanceServer, image string) (api.InstanceSource, error) {
	var remoteErr error
	imageServer, err := incuscli.ConnectSimpleStreams(imageServerURL, nil)
	if err != nil {
		remoteErr = fmt.Errorf("connecting to image server: %w", err)
	} else if alias, _, aerr := imageServer.GetImageAlias(image); aerr != nil {
		remoteErr = aerr
	} else {
		return api.InstanceSource{
			Type:     "image",
			Server:   imageServerURL,
			Protocol: "simplestreams",
			Alias:    alias.Name,
		}, nil
	}

	// The image server is unreachable or dropped this alias. Reuse a copy
	// cached from an earlier launch rather than failing outright.
	if fingerprint, ok := localImage(server, image); ok {
		color.Warn("Image %q unavailable from %s (%v); using locally cached copy %s",
			image, imageServerURL, remoteErr, fingerprint[:12])
		return api.InstanceSource{
			Type:        "image",
			Fingerprint: fingerprint,
		}, nil
	}

	return api.InstanceSource{}, fmt.Errorf("finding image %q: %w", image, remoteErr)
}

// localImage returns the fingerprint of the most recently cached image
// matching image (e.g. "fedora/44" or "images:ubuntu/24.04"), ignoring any
// remote prefix. ok is false when nothing matches.
func localImage(server incuscli.InstanceServer, image string) (fingerprint string, ok bool) {
	distro, release, ok := splitImage(image)
	if !ok {
		return "", false
	}

	images, err := server.GetImages()
	if err != nil {
		return "", false
	}

	var best api.Image
	for _, img := range images {
		if !strings.EqualFold(img.Properties["os"], distro) || img.Properties["release"] != release {
			continue
		}
		if fingerprint == "" || img.UploadedAt.After(best.UploadedAt) {
			best = img
			fingerprint = img.Fingerprint
		}
	}
	return fingerprint, fingerprint != ""
}

// splitImage splits a reference like "fedora/44" or "images:ubuntu/24.04" into
// its distro and release, dropping any "remote:" prefix.
func splitImage(image string) (distro, release string, ok bool) {
	if _, after, found := strings.Cut(image, ":"); found {
		image = after
	}
	distro, release, ok = strings.Cut(image, "/")
	if !ok {
		return "", "", false
	}
	return distro, release, true
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

// BaseMarkerKey marks a container as a base template rather than a project's
// container. Templates are hidden from ListSiloInstances.
const BaseMarkerKey = "user.silo.base"

// TmpMarkerKey marks a throwaway container made by `silo tmp`.
const TmpMarkerKey = "user.silo.tmp"

// ListSiloInstances returns all Incus instances whose name starts with "silo-",
// except base templates.
func ListSiloInstances(server incuscli.InstanceServer) ([]api.Instance, error) {
	instances, err := server.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return nil, fmt.Errorf("listing instances: %w", err)
	}
	var result []api.Instance
	for _, inst := range instances {
		if inst.Config[BaseMarkerKey] == "true" {
			continue
		}
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
