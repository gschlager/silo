package incus

import (
	"context"
	"fmt"
	"sort"
	"strings"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

// SnapshotInfo holds snapshot details.
type SnapshotInfo struct {
	Name      string
	CreatedAt string
	Stateful  bool
}

// CreateSnapshot takes a named snapshot of the container.
func CreateSnapshot(ctx context.Context, server incuscli.InstanceServer, container, name string) error {
	op, err := server.CreateInstanceSnapshot(container, api.InstanceSnapshotsPost{
		Name: name,
	})
	if err != nil {
		return fmt.Errorf("creating snapshot %q of %q: %w", name, container, err)
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

// RestoreSnapshot restores a container to a named snapshot.
func RestoreSnapshot(ctx context.Context, server incuscli.InstanceServer, container, name string) error {
	inst, etag, err := server.GetInstance(container)
	if err != nil {
		return fmt.Errorf("getting container %q: %w", container, err)
	}

	writable := inst.Writable()
	writable.Restore = name

	op, err := server.UpdateInstance(container, writable, etag)
	if err != nil {
		return fmt.Errorf("restoring snapshot %q of %q: %w", name, container, err)
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

// ListSnapshots returns all snapshots for a container.
func ListSnapshots(server incuscli.InstanceServer, container string) ([]SnapshotInfo, error) {
	snapshots, err := server.GetInstanceSnapshots(container)
	if err != nil {
		return nil, fmt.Errorf("listing snapshots of %q: %w", container, err)
	}

	var result []SnapshotInfo
	for _, s := range snapshots {
		result = append(result, SnapshotInfo{
			Name:      s.Name,
			CreatedAt: s.CreatedAt.Format("2006-01-02 15:04:05"),
			Stateful:  s.Stateful,
		})
	}
	return result, nil
}

// CleanupSnapshots deletes old snapshots whose names start with the given
// prefix, keeping the most recent `keep` snapshots. Errors are silently
// ignored (best-effort cleanup).
func CleanupSnapshots(ctx context.Context, server incuscli.InstanceServer, container, prefix string, keep int) {
	snapshots, err := server.GetInstanceSnapshots(container)
	if err != nil {
		return
	}

	var matching []api.InstanceSnapshot
	for _, s := range snapshots {
		if strings.HasPrefix(s.Name, prefix) {
			matching = append(matching, s)
		}
	}

	if len(matching) <= keep {
		return
	}

	sort.Slice(matching, func(i, j int) bool {
		return matching[i].CreatedAt.Before(matching[j].CreatedAt)
	})

	for _, s := range matching[:len(matching)-keep] {
		DeleteSnapshot(ctx, server, container, s.Name)
	}
}

// DeleteSnapshot removes a snapshot from a container.
func DeleteSnapshot(ctx context.Context, server incuscli.InstanceServer, container, name string) error {
	op, err := server.DeleteInstanceSnapshot(container, name)
	if err != nil {
		return fmt.Errorf("deleting snapshot %q of %q: %w", name, container, err)
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
