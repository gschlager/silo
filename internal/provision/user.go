package provision

import (
	"context"
	"fmt"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/incus"
)

// CreateUser creates the dev user inside the container with the given shell.
func CreateUser(ctx context.Context, server incuscli.InstanceServer, container, username, shell string) error {
	rootOpts := incus.ExecOpts{}

	// Create user with home directory.
	if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
		"useradd", "-m", "-s", "/bin/" + shell, username,
	}); err != nil {
		return fmt.Errorf("creating user %q: %w", username, err)
	}

	// Add to sudo group (wheel on Fedora/RHEL, sudo on Debian/Ubuntu).
	// Try wheel first, fall back to sudo.
	if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
		"usermod", "-aG", "wheel", username,
	}); err != nil {
		// Try sudo group instead.
		incus.Exec(ctx, server, container, rootOpts, []string{
			"usermod", "-aG", "sudo", username,
		})
	}

	// Allow passwordless sudo.
	if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
		"sh", "-c", fmt.Sprintf(`echo "%s ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/%s`, username, username),
	}); err != nil {
		return fmt.Errorf("configuring sudo for %q: %w", username, err)
	}

	// Add ~/.local/bin to PATH in ~/.profile (sourced by all login shells).
	pathLine := `export PATH="$HOME/.local/bin:$PATH"`
	userOpts := incus.ExecOpts{User: 1000, Home: "/home/" + username}
	if _, err := incus.Exec(ctx, server, container, userOpts, []string{
		"sh", "-c", fmt.Sprintf(`echo '%s' >> ~/.profile`, pathLine),
	}); err != nil {
		return fmt.Errorf("setting PATH for %q: %w", username, err)
	}

	// Enable systemd user linger so user services start at boot.
	if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
		"loginctl", "enable-linger", username,
	}); err != nil {
		return fmt.Errorf("enabling linger for %q: %w", username, err)
	}

	return nil
}
