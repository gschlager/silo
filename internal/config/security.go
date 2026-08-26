package config

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
)

// canonicalProjectPath returns the stable host identity used for project-scoped
// containers, state, and secrets. Resolving symlinks prevents the same checkout
// from acquiring multiple identities through different host paths.
func canonicalProjectPath(projectDir string) string {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		abs = filepath.Clean(projectDir)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

// validPathIdentifier accepts a single portable path component. Agent and mode
// names become parts of host-side paths, so separators and dot components must
// never be accepted.
func validPathIdentifier(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

// ValidateAgentModePath validates values used to construct the host-side agent
// mode directory.
func ValidateAgentModePath(agent, mode string) error {
	if !validPathIdentifier(agent) {
		return fmt.Errorf("invalid agent name %q: use only letters, digits, '.', '_' and '-'", agent)
	}
	// An empty mode is retained for backwards compatibility with custom agents;
	// it resolves to the agent's own contained directory and cannot traverse.
	if mode != "" && !validPathIdentifier(mode) {
		return fmt.Errorf("invalid mode name %q for agent %q: use only letters, digits, '.', '_' and '-'", mode, agent)
	}
	return nil
}

func projectIdentitySuffix(projectDir string) string {
	sum := sha256.Sum256([]byte(canonicalProjectPath(projectDir)))
	return fmt.Sprintf("%x", sum[:8])
}
