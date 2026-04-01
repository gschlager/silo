package config

import (
	"path/filepath"
	"strings"
)

// MergedConfig is the fully resolved configuration used by all commands.
type MergedConfig struct {
	// Container settings.
	Image         string
	ContainerName string
	ProjectDir    string

	// Command lists.
	DefaultSetup []string
	Setup        []string
	Sync         []string
	Reset        map[string][]string
	Update       []string

	// Environment and networking.
	Ports  []string
	Env    map[string]string
	Mounts []string

	// Git configuration.
	Git        map[string]string
	GitCredential *CredentialConfig

	// Agent configuration (merged: project replaces global per agent).
	Agents map[string]MergedAgentConfig

	// Tools.
	Tools map[string]ToolConfig

	// Daemons.
	Daemons map[string]DaemonConfig

	// Docker/Compose.
	Docker  bool
	Compose string

	// Global settings.
	Shell         string
	User          string
	Notifications bool
}

// MergedAgentConfig combines global and project agent settings.
type MergedAgentConfig struct {
	Mode string
	Home string
	Seed AgentSeedConfig
	Env  map[string]string
}

// Merge combines global and project configs into a single resolved config.
// projectDir is the absolute path to the project directory.
func Merge(global *GlobalConfig, project *ProjectConfig, projectDir string) *MergedConfig {
	m := &MergedConfig{
		ContainerName: ContainerName(projectDir),
		ProjectDir:    projectDir,
		Shell:         global.Shell,
		User:          global.User,
		Notifications: global.Notifications,
	}

	// Image: project overrides global default.
	if project != nil && project.Image != "" {
		m.Image = project.Image
	} else {
		m.Image = global.DefaultImage
	}

	// DefaultSetup: only runs if project uses the default image.
	useDefaultSetup := project == nil || project.Image == "" || project.Image == global.DefaultImage
	if useDefaultSetup {
		m.DefaultSetup = global.DefaultSetup
	}

	// Command lists: project-level only.
	if project != nil {
		m.Setup = project.Setup
		m.Sync = project.Sync
		m.Reset = project.Reset
		m.Update = project.Update
		m.Ports = project.Ports
		m.Docker = project.Docker
		m.Compose = project.Compose
	}

	// Env: project-level only.
	if project != nil && project.Env != nil {
		m.Env = project.Env
	}

	// Mounts: union of global and project.
	m.Mounts = append(m.Mounts, global.Mounts...)
	if project != nil {
		m.Mounts = append(m.Mounts, project.Mounts...)
	}

	// Git: global base, project overrides individual keys.
	m.Git = make(map[string]string)
	for k, v := range global.Git {
		m.Git[k] = v
	}
	if project != nil {
		for k, v := range project.Git.Settings {
			if k == "credential" {
				continue // handled separately
			}
			m.Git[k] = v
		}
		m.GitCredential = project.Git.Credential
	}

	// Agents: project replaces global entirely per agent.
	m.Agents = make(map[string]MergedAgentConfig)
	for name, ga := range global.Agents {
		m.Agents[name] = MergedAgentConfig{
			Mode: ga.Mode,
			Home: ga.Home,
			Seed: ga.Seed,
		}
	}
	if project != nil {
		for name, pa := range project.Agents {
			merged := MergedAgentConfig{
				Mode: pa.Mode,
				Env:  pa.Env,
			}
			// Keep home and seed from global if this agent exists there.
			if ga, ok := global.Agents[name]; ok {
				merged.Home = ga.Home
				merged.Seed = ga.Seed
			}
			if merged.Mode == "" {
				if ga, ok := global.Agents[name]; ok {
					merged.Mode = ga.Mode
				}
			}
			m.Agents[name] = merged
		}
	}

	// Tools: project-level only.
	if project != nil {
		m.Tools = project.Tools
	}

	// Daemons: project-level only.
	if project != nil {
		m.Daemons = project.Daemons
	}

	return m
}

// ContainerName derives the container name from the project directory.
func ContainerName(projectDir string) string {
	return "silo-" + sanitizeName(filepath.Base(projectDir))
}

// sanitizeName replaces characters that are invalid in Incus container names.
func sanitizeName(name string) string {
	// Incus names: alphanumeric and hyphens, must start with a letter.
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else if r == '_' || r == '.' || r == ' ' {
			b.WriteRune('-')
		}
	}
	result := b.String()
	// Ensure it starts with a letter.
	if len(result) > 0 && !((result[0] >= 'a' && result[0] <= 'z') || (result[0] >= 'A' && result[0] <= 'Z')) {
		result = "s" + result
	}
	if result == "" {
		result = "silo"
	}
	return result
}
