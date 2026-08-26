package config

import (
	"bytes"
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

// SecretsMigrationResult describes the outcome of a one-time project key
// migration without treating an already-migrated or absent key as an error.
type SecretsMigrationResult int

const (
	SecretsLegacyMissing SecretsMigrationResult = iota
	SecretsAlreadyCurrent
	SecretsMigrated
)

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
	return SecretsForProjects(project)
}

// SecretsForProjects returns the first configured entry from a list of aliases.
// This keeps hash-suffixed entries readable until migrate-secrets moves them
// back to the registry-assigned readable name.
func SecretsForProjects(projects ...string) (map[string]string, error) {
	s, err := LoadSecrets()
	if err != nil {
		return nil, err
	}
	for _, project := range projects {
		if m, ok := s[project]; ok {
			if m != nil {
				return m, nil
			}
		}
	}
	return map[string]string{}, nil
}

// MigrateSecretsProjectKey atomically renames one top-level project key while
// preserving YAML comments and ordering. It never merges or overwrites entries:
// if both keys exist, the user must resolve the conflict explicitly.
func MigrateSecretsProjectKey(legacyKey, currentKey string) (SecretsMigrationResult, error) {
	if legacyKey == currentKey {
		return SecretsAlreadyCurrent, nil
	}

	path := SecretsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SecretsLegacyMissing, nil
		}
		return SecretsLegacyMissing, fmt.Errorf("reading %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return SecretsLegacyMissing, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return SecretsLegacyMissing, fmt.Errorf("parsing %s: expected a top-level mapping", path)
	}

	root := doc.Content[0]
	var legacyNode *yaml.Node
	legacyIndex := -1
	currentExists := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		switch key.Value {
		case legacyKey:
			legacyNode = key
			legacyIndex = i
		case currentKey:
			currentExists = true
		}
	}

	if currentExists {
		if legacyNode != nil {
			value := root.Content[legacyIndex+1]
			if (value.Kind == yaml.ScalarNode && value.Tag == "!!null") ||
				(value.Kind == yaml.MappingNode && len(value.Content) == 0) {
				root.Content = append(root.Content[:legacyIndex], root.Content[legacyIndex+2:]...)
				if err := writeSecretsDocument(path, &doc); err != nil {
					return SecretsAlreadyCurrent, err
				}
				return SecretsMigrated, nil
			}
			return SecretsAlreadyCurrent, fmt.Errorf("both legacy key %q and current key %q exist in %s; merge them manually", legacyKey, currentKey, path)
		}
		return SecretsAlreadyCurrent, nil
	}
	if legacyNode == nil {
		return SecretsLegacyMissing, nil
	}

	legacyNode.Value = currentKey
	legacyNode.Tag = "!!str"
	if err := writeSecretsDocument(path, &doc); err != nil {
		return SecretsLegacyMissing, err
	}
	return SecretsMigrated, nil
}

func writeSecretsDocument(path string, doc *yaml.Node) error {
	var output bytes.Buffer
	enc := yaml.NewEncoder(&output)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := writeFileAtomic(path, output.Bytes(), 0600); err != nil {
		return err
	}
	return nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".silo-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary file for %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if err = tmp.Chmod(mode); err != nil {
		return fmt.Errorf("setting permissions on temporary file for %s: %w", path, err)
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("writing temporary file for %s: %w", path, err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("syncing temporary file for %s: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("closing temporary file for %s: %w", path, err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// EnsureSecretsStub appends a commented stub block for project if the file has
// no entry for it yet, giving users an obvious place to add the project's PAT.
// Existing entries (including a previously added stub, which parses as a null
// value) are left untouched so user edits and comments are preserved. Returns
// true when a stub was added.
func EnsureSecretsStub(project string) (bool, error) {
	return EnsureSecretsStubForProject(project)
}

// EnsureSecretsStubForProject adds a stub for project only when none of its
// previous aliases already exists.
func EnsureSecretsStubForProject(project string, aliases ...string) (bool, error) {
	s, err := LoadSecrets()
	if err != nil {
		return false, err
	}
	for _, key := range append([]string{project}, aliases...) {
		if _, ok := s[key]; ok {
			return false, nil
		}
	}
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
