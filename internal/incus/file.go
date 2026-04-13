package incus

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	incuscli "github.com/lxc/incus/v6/client"
)

// PushFile copies a file or directory from the host to the container.
func PushFile(server incuscli.InstanceServer, container, src, dst string, recursive bool) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %q: %w", src, err)
	}

	if info.IsDir() {
		if !recursive {
			return fmt.Errorf("%q is a directory (use -r for recursive copy)", src)
		}
		return pushDir(server, container, src, dst)
	}

	return pushOneFile(server, container, src, dst, info)
}

// PullFile copies a file or directory from the container to the host.
func PullFile(server incuscli.InstanceServer, container, src, dst string, recursive bool) error {
	content, resp, err := server.GetInstanceFile(container, src)
	if err != nil {
		return fmt.Errorf("get %q from %q: %w", src, container, err)
	}

	if resp.Type == "directory" {
		if content != nil {
			content.Close()
		}
		if !recursive {
			return fmt.Errorf("%q is a directory (use -r for recursive copy)", src)
		}
		return pullDir(server, container, src, dst)
	}

	defer content.Close()
	return writeFile(dst, content, os.FileMode(resp.Mode))
}

func pushOneFile(server incuscli.InstanceServer, container, src, dst string, info os.FileInfo) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	return server.CreateInstanceFile(container, dst, incuscli.InstanceFileArgs{
		Content: f,
		Mode:    int(info.Mode()),
		Type:    "file",
	})
}

func pushDir(server incuscli.InstanceServer, container, src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		// Use forward slashes for container paths.
		target = strings.ReplaceAll(target, string(os.PathSeparator), "/")

		if info.IsDir() {
			return server.CreateInstanceFile(container, target, incuscli.InstanceFileArgs{
				Type: "directory",
				Mode: int(info.Mode()),
			})
		}

		return pushOneFile(server, container, path, target, info)
	})
}

func pullDir(server incuscli.InstanceServer, container, src, dst string) error {
	// List the directory by reading it (returns entries as lines).
	content, _, err := server.GetInstanceFile(container, src)
	if err != nil {
		return fmt.Errorf("list %q: %w", src, err)
	}

	data, err := io.ReadAll(content)
	content.Close()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	entries := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		remotePath := src + "/" + entry
		localPath := filepath.Join(dst, entry)

		childContent, childResp, err := server.GetInstanceFile(container, remotePath)
		if err != nil {
			return fmt.Errorf("get %q: %w", remotePath, err)
		}

		if childResp.Type == "directory" {
			if childContent != nil {
				childContent.Close()
			}
			if err := pullDir(server, container, remotePath, localPath); err != nil {
				return err
			}
		} else {
			if err := writeFile(localPath, childContent, os.FileMode(childResp.Mode)); err != nil {
				childContent.Close()
				return err
			}
			childContent.Close()
		}
	}

	return nil
}

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, r)
	return err
}
