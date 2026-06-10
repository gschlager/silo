package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SecretsPath returns the path to the central secrets file.
func SecretsPath() string {
	return filepath.Join(GlobalConfigDir(), "secrets.yml")
}

// Secrets maps a project name to its named secrets (env var name -> source). A
// source is a 1Password reference (op://…) or a literal value. The reserved name
// "github" wires the git credential helper and exports GH_TOKEN in addition to
// GITHUB_TOKEN; any other name becomes a plain environment variable.
type Secrets map[string]map[string]string

// LoadSecrets reads the central secrets file, returning an empty set if it does
// not exist.
func LoadSecrets() (Secrets, error) {
	data, err := os.ReadFile(SecretsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return Secrets{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", SecretsPath(), err)
	}
	var s Secrets
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", SecretsPath(), err)
	}
	if s == nil {
		s = Secrets{}
	}
	return s, nil
}

// SecretsForProject returns the secrets configured for a project, or an empty
// map if none are set.
func SecretsForProject(project string) (map[string]string, error) {
	s, err := LoadSecrets()
	if err != nil {
		return nil, err
	}
	if m, ok := s[project]; ok && m != nil {
		return m, nil
	}
	return map[string]string{}, nil
}

// EnsureSecretsStub appends a commented stub block for project if the file has
// no entry for it yet, giving users an obvious place to add the project's PAT.
// Existing entries (including a previously added stub, which parses as a null
// value) are left untouched so user edits and comments are preserved. Returns
// true when a stub was added.
func EnsureSecretsStub(project string) (bool, error) {
	stub := fmt.Sprintf("%s:\n  # github: op://vault/item/field   # GitHub PAT (wires git + gh)\n\n", project)
	return appendProjectBlock(project, stub)
}

// AddProjectSecret appends "project:\n  name: value" to the secrets file when the
// project has no entry yet, returning true on success. If an entry already
// exists it makes no change and returns false, so callers can fall back to a
// manual hint rather than clobbering existing entries or comments.
func AddProjectSecret(project, name, value string) (bool, error) {
	block := fmt.Sprintf("%s:\n  %s: %s\n\n", project, name, value)
	return appendProjectBlock(project, block)
}

// appendProjectBlock appends block to the secrets file unless project already
// has an entry. Writes a header the first time the file is created.
func appendProjectBlock(project, block string) (bool, error) {
	s, err := LoadSecrets()
	if err != nil {
		return false, err
	}
	if _, ok := s[project]; ok {
		return false, nil
	}

	path := SecretsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return false, fmt.Errorf("creating config directory: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return false, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	// Write a header the first time the file is created.
	if info, err := f.Stat(); err == nil && info.Size() == 0 {
		header := "# silo per-project secrets — injected as environment variables.\n" +
			"# Each value is a 1Password reference (op://vault/item/field) or a literal.\n" +
			"# The reserved 'github' key wires the git credential helper and GH_TOKEN.\n\n"
		if _, err := f.WriteString(header); err != nil {
			return false, fmt.Errorf("writing %s: %w", path, err)
		}
	}

	if _, err := f.WriteString(block); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}
