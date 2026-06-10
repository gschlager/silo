package provision

import (
	"context"
	"fmt"

	incuscli "github.com/lxc/incus/v6/client"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/incus"
)

// EnsureShell makes sure the configured login shell exists inside the container
// and returns the shell silo should actually use.
//
// silo runs every command through `/bin/<shell> -lc` (setup, run, daemons,
// enter), so the shell must be present regardless of the image or whether the
// project overrode default_setup. bash is part of every base image; any other
// shell is installed on demand via dnf. If installation fails, EnsureShell warns
// and falls back to bash so provisioning can continue with a shell that is
// guaranteed to work. The returned shell must be used for the rest of
// provisioning (user creation, daemons) and persisted so later commands agree.
func EnsureShell(ctx context.Context, server incuscli.InstanceServer, container, shell string) (string, error) {
	rootOpts := incus.ExecOpts{}

	present := func(sh string) bool {
		_, err := incus.Exec(ctx, server, container, rootOpts, []string{
			"sh", "-c", "command -v " + sh + " >/dev/null 2>&1",
		})
		return err == nil
	}

	if present(shell) {
		return shell, nil
	}

	if shell != "bash" {
		status("Installing %s...", shell)
		if _, err := incus.Exec(ctx, server, container, rootOpts, []string{
			"sh", "-c", "dnf install -y " + shell,
		}); err == nil && present(shell) {
			return shell, nil
		}
		color.Warn("could not install shell %q; falling back to bash", shell)
	}

	if present("bash") {
		return "bash", nil
	}
	return "", fmt.Errorf("neither %q nor bash is available in %s", shell, container)
}

// configureInputrc enables prefix history search for bash line editing: type a
// few characters, press Up/Down, and cycle only through matching history. This
// is the main interactive nicety users expect, delivered without oh-my-zsh.
// Appended to /etc/inputrc (read by bash for all users); harmless under zsh,
// which ignores inputrc.
func configureInputrc(ctx context.Context, server incuscli.InstanceServer, container string) error {
	rootOpts := incus.ExecOpts{}
	content := "\n# Added by silo: prefix history search on Up/Down.\n" +
		"\"\\e[A\": history-search-backward\n" +
		"\"\\e[B\": history-search-forward\n"
	if _, err := incus.ExecWithStdin(ctx, server, container, rootOpts, []string{
		"tee", "-a", "/etc/inputrc",
	}, []byte(content)); err != nil {
		return fmt.Errorf("configuring inputrc: %w", err)
	}
	return nil
}
