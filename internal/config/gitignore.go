package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// GitignorePath returns the path to the global gitignore applied in every container.
func GitignorePath() string {
	return filepath.Join(GlobalConfigDir(), "gitignore")
}

const defaultGitignore = `# silo global gitignore — applied in every container as ~/.config/git/ignore.
# Edit this file to change what git ignores across all your silo containers;
# changes apply on the next 'silo up' or 'silo enter'.

.silo.yml
.silo.local.yml
.idea/
.vscode/
.claude/
.codex/
.DS_Store
`

// EnsureGitignore creates the global gitignore with sensible defaults if it does
// not exist yet.
func EnsureGitignore() error {
	path := GitignorePath()
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(defaultGitignore), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// LoadGitignore returns the global gitignore content, or nil if the file is absent.
func LoadGitignore() ([]byte, error) {
	data, err := os.ReadFile(GitignorePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", GitignorePath(), err)
	}
	return data, nil
}
