package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gschlager/silo/internal/cache"
	"github.com/gschlager/silo/internal/color"
	"github.com/spf13/cobra"
)

func newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage build caches",
	}

	cmd.AddCommand(
		newCacheListCmd(),
		newCacheCleanCmd(),
	)
	return cmd
}

func newCacheListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List caches with sizes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cacheBase := filepath.Join(cache.DataDir(), "cache")
			if _, err := os.Stat(cacheBase); os.IsNotExist(err) {
				fmt.Println("No caches.")
				return nil
			}

			found := false
			err := filepath.Walk(cacheBase, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				// Show directories that are two levels deep (container/cache-name or shared/cache-name).
				rel, _ := filepath.Rel(cacheBase, path)
				depth := len(strings.Split(rel, string(os.PathSeparator)))
				if info.IsDir() && depth == 2 && rel != "." {
					size := dirSize(path)
					fmt.Printf("%-50s  %s\n", rel, formatSize(size))
					found = true
				}
				return nil
			})
			if err != nil {
				return err
			}
			if !found {
				fmt.Println("No caches.")
			}
			return nil
		},
	}
}

func newCacheCleanCmd() *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "clean [container-name]",
		Short: "Remove caches",
		Long: `Remove build caches.

Without arguments, removes caches for the current project's container.
With --all, removes all caches (per-project and shared).
With a container name, removes caches for that specific container.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cacheBase := filepath.Join(cache.DataDir(), "cache")

			if all {
				color.Status("Removing all caches...")
				if err := os.RemoveAll(cacheBase); err != nil {
					return fmt.Errorf("removing caches: %w", err)
				}
				color.Success("Done.")
				return nil
			}

			// Determine which container's cache to clean.
			var containerName string
			if len(args) > 0 {
				containerName = args[0]
			} else {
				cfg, err := loadConfig()
				if err != nil {
					return err
				}
				containerName = cfg.ContainerName
			}

			cacheDir := filepath.Join(cacheBase, containerName)
			if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
				color.Info("No caches for %s.", containerName)
				return nil
			}

			color.Status("Removing caches for %s...", containerName)
			if err := os.RemoveAll(cacheDir); err != nil {
				return fmt.Errorf("removing caches: %w", err)
			}
			color.Success("Done.")
			return nil
		},
	}

	cmd.Flags().BoolVarP(&all, "all", "a", false, "Remove all caches (per-project and shared)")
	return cmd
}

func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
