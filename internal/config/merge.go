package config

import (
	"maps"
	"os"
	"path/filepath"
	"strconv"
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
	Use          UseList
	Setup        []string
	Sync         []string
	Reset        map[string][]string
	Update       []string

	// Environment and networking.
	Ports  []PortForward
	Env    map[string]string
	Mounts []string

	// Git configuration.
	Git        map[string]string
	GitCredential *CredentialConfig

	// Agent configuration (merged: project replaces global per agent).
	Agents     map[string]MergedAgentConfig
	AgentOrder []string // preserves definition order from global config

	// Tools.
	Tools map[string]ToolConfig

	// Daemons.
	Daemons map[string]DaemonConfig

	// Container nesting (required for Docker, Podman, etc.).
	Nesting bool

	// Global settings.
	Shell         string
	User          string
	DefaultAgent  string
	PassEnv       []string
	Notifications bool
}

// MergedAgentConfig combines global and project agent settings.
type MergedAgentConfig struct {
	Cmd     string
	Deps    []string
	Install string
	Mode    string
	Links   []LinkRule
	Env     map[string]string
	Enabled bool
}

// ResolveTarget expands ~/ in the target path to the user home directory.
func (r LinkRule) ResolveTarget(userHome string) string {
	if strings.HasPrefix(r.Target, "~/") {
		return filepath.Join(userHome, r.Target[2:])
	}
	return r.Target
}

// AgentCmd returns the launch command for an agent. If Cmd is set, it's
// used as-is. Otherwise falls back to the agent name.
func (a *MergedAgentConfig) AgentCmd(name string) string {
	if a.Cmd != "" {
		return a.Cmd
	}
	return name
}

// HostEnv returns a map of host environment variables that should be
// passed to interactive container sessions, based on the PassEnv config.
func (m *MergedConfig) HostEnv() map[string]string {
	env := make(map[string]string)
	for _, key := range m.PassEnv {
		if v := os.Getenv(key); v != "" {
			env[key] = v
		}
	}
	return env
}

// UserHome returns the home directory for the configured user.
func (m *MergedConfig) UserHome() string {
	return "/home/" + m.User
}

// ProjectName returns the short project name used to key the central secrets
// file. It matches the container name without the "silo-" prefix.
func (m *MergedConfig) ProjectName() string {
	return strings.TrimPrefix(m.ContainerName, "silo-")
}

// ResolveDefaultAgent returns the default agent name. If DefaultAgent is set,
// it returns that. Otherwise it returns the first agent in definition order.
func (m *MergedConfig) ResolveDefaultAgent() string {
	if m.DefaultAgent != "" {
		return m.DefaultAgent
	}
	if len(m.AgentOrder) > 0 {
		return m.AgentOrder[0]
	}
	return ""
}

// ShellPath returns the absolute path to the configured shell.
func (m *MergedConfig) ShellPath() string {
	return "/bin/" + m.Shell
}

// LoginCmd returns a login shell command that executes the given command string.
func (m *MergedConfig) LoginCmd(cmd string) []string {
	return []string{m.ShellPath(), "-lc", cmd}
}

// WorkspacePath returns the container-side workspace path for this project.
// Each project gets its own subdirectory under /workspace/ to avoid config
// collisions in agents that key settings by workspace path.
func (m *MergedConfig) WorkspacePath() string {
	return "/workspace/" + filepath.Base(m.ProjectDir)
}

// Merge combines global and project configs into a single resolved config.
// projectDir is the absolute path to the project directory.
func Merge(global *GlobalConfig, project *ProjectConfig, projectDir string) *MergedConfig {
	m := &MergedConfig{
		ContainerName: ContainerName(projectDir),
		ProjectDir:    projectDir,
		Shell:         global.Shell,
		User:          global.User,
		DefaultAgent:  global.DefaultAgent,
		PassEnv:       global.PassEnv,
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
		m.Use = project.Use
		m.Setup = project.Setup
		m.Sync = project.Sync
		m.Reset = project.Reset
		m.Update = project.Update
		m.Ports = project.Ports
		m.Nesting = project.Nesting
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
	m.Git = maps.Clone(global.Git)
	if m.Git == nil {
		m.Git = make(map[string]string)
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

	// Agents: build from global (preserving order), project overrides per agent.
	m.Agents = make(map[string]MergedAgentConfig)
	globalAgents := make(map[string]AgentGlobalConfig)
	for _, ga := range global.Agents {
		m.AgentOrder = append(m.AgentOrder, ga.Name)
		globalAgents[ga.Name] = ga
		m.Agents[ga.Name] = MergedAgentConfig{
			Cmd:     ga.Cmd,
			Deps:    ga.Deps,
			Install: ga.Install,
			Mode:    ga.Mode,
			Links:   ga.Links,
			Enabled: true,
		}
	}
	if project != nil {
		for name, pa := range project.Agents {
			merged := MergedAgentConfig{
				Mode:    pa.Mode,
				Env:     pa.Env,
				Enabled: true,
			}
			if pa.Enabled != nil {
				merged.Enabled = *pa.Enabled
			}
			// Keep deps, install, links and set from global if this agent exists there.
			if ga, ok := globalAgents[name]; ok {
				merged.Cmd = ga.Cmd
				merged.Deps = ga.Deps
				merged.Install = ga.Install
				merged.Links = ga.Links
			}
			if merged.Mode == "" {
				if ga, ok := globalAgents[name]; ok {
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

	// Daemons: project-level only. Collect daemon ports into Ports, skipping
	// any whose container port is already forwarded. The same port may be
	// declared both at the top level and on a daemon; forwarding it twice
	// would try to bind the host port more than once and fail.
	if project != nil {
		m.Daemons = project.Daemons
		seen := make(map[int]bool)
		for _, pf := range m.Ports {
			if cp, ok := containerPort(pf.Spec); ok {
				seen[cp] = true
			}
		}
		for _, daemon := range project.Daemons {
			for _, pf := range daemon.Ports {
				cp, ok := containerPort(pf.Spec)
				if ok && seen[cp] {
					continue
				}
				if ok {
					seen[cp] = true
				}
				m.Ports = append(m.Ports, pf)
			}
		}
	}

	return m
}

// containerPort extracts the container port from a port spec like "3000:13000"
// or "3000" (same port on both sides). The second return value is false when the
// spec is malformed; such specs are left for the provisioner to report.
func containerPort(spec string) (int, bool) {
	field := strings.TrimSpace(spec)
	if i := strings.IndexByte(field, ':'); i >= 0 {
		field = field[:i]
	}
	n, err := strconv.Atoi(strings.TrimSpace(field))
	if err != nil {
		return 0, false
	}
	return n, true
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
