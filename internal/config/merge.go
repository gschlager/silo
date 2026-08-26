package config

import (
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// MergedConfig is the fully resolved configuration used by all commands.
type MergedConfig struct {
	// Container settings.
	Image         string
	ContainerName string
	ProjectDir    string
	ProjectKey    string

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
	Git           map[string]string
	GitCredential *CredentialConfig

	// Agent configuration (merged: project replaces global per agent).
	Agents     map[string]MergedAgentConfig
	AgentOrder []string // preserves definition order from global config

	// Tools.
	Tools map[string]ToolConfig

	// Daemons.
	Daemons map[string]DaemonConfig

	// Presets defined in the global config, for `use:` expansion.
	Presets map[string]UserPreset

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
	Env     map[string]string     // project-level env overrides
	Modes   map[string]ModeConfig // per-mode env, from global config
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

// ModeEnvKeys returns the sorted names of the environment variables the agent's
// active mode injects. Names only — values may be secrets or op:// references,
// so they're never returned for display.
func (a *MergedAgentConfig) ModeEnvKeys() []string {
	mc, ok := a.Modes[a.Mode]
	if !ok || len(mc.Env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(mc.Env))
	for k := range mc.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

// ProjectName returns the stable, path-scoped key used for secrets and other
// host-owned project policy. It is intentionally independent of the readable
// container name.
func (m *MergedConfig) ProjectName() string {
	if m.ProjectKey != "" {
		return m.ProjectKey
	}
	return ProjectName(m.ProjectDir)
}

// ProjectName returns a readable, path-scoped project key for a directory.
func ProjectName(projectDir string) string {
	base := sanitizeName(filepath.Base(canonicalProjectPath(projectDir)))
	const maxBaseLen = 41 // preserve the key format introduced with path scoping
	if len(base) > maxBaseLen {
		base = base[:maxBaseLen]
	}
	return base + "-" + projectIdentitySuffix(projectDir)
}

// LegacyProjectName returns the basename-only key used before project identity
// hashes were introduced. It exists solely for the explicit secrets migration.
func LegacyProjectName(projectDir string) string {
	return sanitizeName(filepath.Base(projectDir))
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
	projectDir = canonicalProjectPath(projectDir)
	m := &MergedConfig{
		ContainerName: ContainerName(projectDir),
		ProjectDir:    projectDir,
		ProjectKey:    ProjectName(projectDir),
		Shell:         global.Shell,
		User:          global.User,
		DefaultAgent:  global.DefaultAgent,
		PassEnv:       global.PassEnv,
		Notifications: global.Notifications,
		Presets:       global.Presets,
	}

	// Image: project overrides global default.
	if project != nil && project.Image != "" {
		m.Image = project.Image
	} else {
		m.Image = global.DefaultImage
	}

	// DefaultSetup: runs when the project uses the same distro as the default
	// image. Its commands are package-manager specific, so this covers minor
	// version bumps like fedora/43 -> fedora/44 while still skipping a genuinely
	// different distro (e.g. ubuntu/*) where the dnf commands wouldn't work.
	useDefaultSetup := project == nil || project.Image == "" || sameDistro(project.Image, global.DefaultImage)
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

	// Env: user presets named in `use:` contribute a base, project env: wins.
	usedPresets := usedUserPresets(global, m.Use)
	for _, p := range usedPresets {
		for k, v := range p.Env {
			if m.Env == nil {
				m.Env = make(map[string]string)
			}
			m.Env[k] = v
		}
	}
	if project != nil && project.Env != nil {
		if m.Env == nil {
			m.Env = project.Env
		} else {
			for k, v := range project.Env {
				m.Env[k] = v
			}
		}
	}

	// Host mounts are host-owned policy. Project files are writable from inside
	// the container, so accepting mounts from .silo.yml would let a compromised
	// repository expose arbitrary host paths on the next provision.
	m.Mounts = append(m.Mounts, global.Mounts...)
	m.Mounts = append(m.Mounts, global.ProjectMounts[m.ProjectName()]...)

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
			Modes:   ga.Modes,
			Enabled: ga.Enabled,
		}
	}
	if project != nil {
		for name, pa := range project.Agents {
			merged := MergedAgentConfig{
				Mode: pa.Mode,
				Env:  pa.Env,
			}
			// Inherit cmd/deps/install/links/enabled from the global agent if it
			// exists there; otherwise this is a project-only agent (enabled).
			if ga, ok := globalAgents[name]; ok {
				merged.Cmd = ga.Cmd
				merged.Deps = ga.Deps
				merged.Install = ga.Install
				merged.Links = ga.Links
				merged.Modes = ga.Modes
				merged.Enabled = ga.Enabled
			} else {
				merged.Enabled = true
			}
			// A project-level enabled override wins over the global default.
			if pa.Enabled != nil {
				merged.Enabled = *pa.Enabled
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

	// Daemons: user presets named in `use:` contribute a base, project daemons:
	// win on a name collision. Collect daemon ports into Ports, skipping any
	// whose container port is already forwarded. The same port may be declared
	// both at the top level and on a daemon; forwarding it twice would try to
	// bind the host port more than once and fail.
	for _, p := range usedPresets {
		for name, d := range p.Daemons {
			if m.Daemons == nil {
				m.Daemons = make(map[string]DaemonConfig)
			}
			m.Daemons[name] = d
		}
	}
	if project != nil {
		if m.Daemons == nil {
			m.Daemons = project.Daemons
		} else {
			for name, d := range project.Daemons {
				m.Daemons[name] = d
			}
		}
	}
	if len(m.Daemons) > 0 {
		seen := make(map[int]bool)
		for _, pf := range m.Ports {
			if cp, ok := containerPort(pf.Spec); ok {
				seen[cp] = true
			}
		}
		// Sorted so which daemon claims a port shared by two of them is stable
		// across runs rather than decided by map iteration order.
		daemonNames := make([]string, 0, len(m.Daemons))
		for name := range m.Daemons {
			daemonNames = append(daemonNames, name)
		}
		sort.Strings(daemonNames)
		for _, daemonName := range daemonNames {
			daemon := m.Daemons[daemonName]
			for _, pf := range daemon.Ports {
				cp, ok := containerPort(pf.Spec)
				if ok && seen[cp] {
					continue
				}
				if ok {
					seen[cp] = true
				}
				// Label unnamed daemon ports with the daemon name so status output
				// and Incus device names identify them. Disambiguate by container
				// port when a daemon exposes more than one, to keep names unique.
				if pf.Name == "" {
					pf.Name = daemonName
					if len(daemon.Ports) > 1 && ok {
						pf.Name = daemonName + "-" + strconv.Itoa(cp)
					}
				}
				m.Ports = append(m.Ports, pf)
			}
		}
	}

	return m
}

// usedUserPresets returns the user-defined presets a project opts into, in `use:`
// declaration order. Names that aren't user presets are skipped — they're either
// built-in presets (expanded separately, in the presets package, since those are
// Go code with typed parameters) or a typo, which the presets package reports.
func usedUserPresets(global *GlobalConfig, use UseList) []UserPreset {
	var out []UserPreset
	for _, u := range use {
		if p, ok := global.Presets[u.Name]; ok {
			out = append(out, p)
		}
	}
	return out
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

// sameDistro reports whether two image references name the same distribution
// (e.g. fedora/43 and fedora/44), ignoring the version and any remote prefix.
func sameDistro(a, b string) bool {
	return distroOf(a) == distroOf(b)
}

// distroOf extracts the distribution name from an image reference like
// "fedora/44" or "images:ubuntu/24.04".
func distroOf(image string) string {
	if i := strings.IndexByte(image, ':'); i >= 0 {
		image = image[i+1:]
	}
	if i := strings.IndexByte(image, '/'); i >= 0 {
		image = image[:i]
	}
	return image
}

// ContainerName derives the legacy readable container name. CLI configuration
// loading replaces it with the name assigned by the host-side project registry;
// keeping this as the default preserves compatibility for callers that do not
// need to allocate a name.
func ContainerName(projectDir string) string {
	base := sanitizeName(filepath.Base(canonicalProjectPath(projectDir)))
	const maxBaseLen = 58 // "silo-" plus the base must fit Incus' 63-char limit
	if len(base) > maxBaseLen {
		base = base[:maxBaseLen]
	}
	return "silo-" + base
}

func collisionContainerName(projectDir string) string {
	base := sanitizeName(filepath.Base(canonicalProjectPath(projectDir)))
	const maxBaseLen = 41
	if len(base) > maxBaseLen {
		base = base[:maxBaseLen]
	}
	return "silo-" + base + "-" + projectIdentitySuffix(projectDir)
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
