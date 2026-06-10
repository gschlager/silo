package provision

import (
	"context"
	"fmt"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
)

// ApplyGitignore writes the host-side global gitignore into the container as
// ~/.config/git/ignore — git's default per-user excludes file, which applies to
// every repository for the user. Called on provision and on start/enter so edits
// to the single host file propagate to every container without reprovisioning.
func ApplyGitignore(ctx context.Context, server incuscli.InstanceServer, container, username string) error {
	if err := config.EnsureGitignore(); err != nil {
		return err
	}
	content, err := config.LoadGitignore()
	if err != nil {
		return err
	}

	userOpts := incus.ExecOpts{User: 1000, Home: "/home/" + username}
	if _, err := incus.ExecWithStdin(ctx, server, container, userOpts, []string{
		"sh", "-c", "mkdir -p ~/.config/git && cat > ~/.config/git/ignore",
	}, content); err != nil {
		return fmt.Errorf("applying global gitignore: %w", err)
	}
	return nil
}
