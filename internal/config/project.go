package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProjectConfig represents the .silo.yml project configuration.
type ProjectConfig struct {
	Image   string                        `yaml:"image"`
	Use     UseList                       `yaml:"use,omitempty"`
	Setup   []string                      `yaml:"setup"`
	Sync    []string                      `yaml:"sync"`
	Reset   map[string][]string           `yaml:"reset"`
	Update  []string                      `yaml:"update"`
	Ports   []PortForward                 `yaml:"ports"`
	Env     map[string]string             `yaml:"env"`
	Git     GitConfig                     `yaml:"git"`
	Agents  map[string]AgentProjectConfig `yaml:"agents"`
	Mounts  []string                      `yaml:"mounts"`
	Tools   map[string]ToolConfig         `yaml:"tools"`
	Daemons map[string]DaemonConfig       `yaml:"daemons"`
	Nesting bool                          `yaml:"nesting"`
}

// PresetUse is a single preset invocation with its raw parameters. Params is the
// raw YAML node so each preset can decode its own typed parameters.
type PresetUse struct {
	Name   string
	Params yaml.Node
}

// UseList is the ordered list of presets a project opts into via `use:`. Order
// matters (e.g. ruby must run before a later `bundle install`), so it is parsed
// from the YAML mapping preserving declaration order rather than into a Go map.
type UseList []PresetUse

// UnmarshalYAML parses the `use:` mapping, preserving key order. A bare key
// (e.g. `valkey:`) yields an empty params node. An empty or null `use:` is
// treated as no presets.
func (u *UseList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode && (value.Tag == "!!null" || value.Value == "") {
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("use: must be a mapping of preset name to parameters")
	}
	// MappingNode content is [key0, val0, key1, val1, ...] in document order.
	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i]
		*u = append(*u, PresetUse{Name: key.Value, Params: *value.Content[i+1]})
	}
	return nil
}

// MarshalYAML emits the `use:` mapping form so a parsed config round-trips.
func (u UseList) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, pu := range u {
		key := &yaml.Node{}
		key.SetString(pu.Name)
		val := pu.Params
		if val.Kind == 0 {
			val = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
		}
		node.Content = append(node.Content, key, &val)
	}
	return node, nil
}

// GitConfig holds git settings. Credential is only read to point at silo
// config update: credentials belong in secrets.yml.
type GitConfig struct {
	Settings   map[string]string `yaml:",inline"`
	Credential *yaml.Node        `yaml:"credential,omitempty"`
}

// AgentProjectConfig holds per-project agent settings.
type AgentProjectConfig struct {
	Enabled *bool             `yaml:"enabled"` // nil = use default (true)
	Mode    string            `yaml:"mode"`    // "claude", "console", "bedrock", "vertex", "foundry"
	Env     map[string]string `yaml:"env"`
}

// ToolConfig is a tools.<name> entry. Its credential moved to secrets.yml;
// it's only read to point at silo config update.
type ToolConfig struct {
	Credential *yaml.Node `yaml:"credential,omitempty"`
}

// DaemonConfig holds daemon configuration, supporting both string and object forms.
type DaemonConfig struct {
	Cmd       string        `yaml:"cmd"`
	Autostart bool          `yaml:"autostart"`
	After     string        `yaml:"after"`
	Ports     []PortForward `yaml:"ports"`

	// Env is injected into this daemon only. Values are literals or op://
	// references resolved on the host at start time, matching agents[].modes.env.
	// Unlike the project-wide env:, which every daemon sees through the systemd
	// user manager, these are scoped to one unit — so two daemons can hold
	// different values for the same variable name.
	Env map[string]string `yaml:"env"`
}

// PortForward is a single port forward. In YAML it accepts either the shorthand
// string form ("5432:15432" or "3000") or a mapping form so the forward can be
// named for clearer status output and Incus device names:
//
//	ports:
//	  - 5432:15432
//	  - name: web
//	    port: 9292:9292
//	    http: true
type PortForward struct {
	Name string // optional label; drives status display and the device name
	Spec string // "container:host" or "container"
	// HTTP marks the forward as a web server, so `silo status --json` reports
	// a URL for it. Ports without it (e.g. Postgres) are just forwards.
	HTTP bool
	// Daemon is the daemon this forward belongs to. Not read from YAML; Merge
	// sets it from the daemons: block, including when a top-level entry
	// already forwards the daemon's port.
	Daemon string
}

// UnmarshalYAML accepts a bare port spec string or a {name, port} mapping.
func (p *PortForward) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		// Use the raw value so both "5432:15432" (string) and 3000 (int) work.
		p.Spec = value.Value
		return nil
	case yaml.MappingNode:
		var raw struct {
			Name string    `yaml:"name"`
			Port yaml.Node `yaml:"port"`
			HTTP bool      `yaml:"http"`
		}
		if err := value.Decode(&raw); err != nil {
			return err
		}
		if raw.Port.Kind != yaml.ScalarNode || raw.Port.Value == "" {
			return fmt.Errorf("port forward %q: 'port' must be a spec like 5432:15432", raw.Name)
		}
		p.Name = raw.Name
		p.Spec = raw.Port.Value
		p.HTTP = raw.HTTP
		return nil
	default:
		return fmt.Errorf("invalid port forward entry")
	}
}

// MarshalYAML writes the shorthand string form when it carries nothing but the
// spec and the mapping form otherwise, so generated configs stay compact.
func (p PortForward) MarshalYAML() (any, error) {
	if p.Name == "" && !p.HTTP {
		return p.Spec, nil
	}
	return struct {
		Name string `yaml:"name,omitempty"`
		Port string `yaml:"port"`
		HTTP bool   `yaml:"http,omitempty"`
	}{p.Name, p.Spec, p.HTTP}, nil
}

// ContainerPort returns the container side of the spec, or false if malformed.
func (p PortForward) ContainerPort() (int, bool) {
	return containerPort(p.Spec)
}

// HostPort returns the host side of the spec ("3000" means the same port on
// both sides), or false if malformed.
func (p PortForward) HostPort() (int, bool) {
	field := p.Spec
	if i := strings.IndexByte(field, ':'); i >= 0 {
		field = field[i+1:]
	}
	n, err := strconv.Atoi(strings.TrimSpace(field))
	if err != nil {
		return 0, false
	}
	return n, true
}

// DeviceName returns the Incus proxy-device name for this forward. Named forwards
// use their sanitized name; unnamed ones use the host port (unique per forward).
// Both are stable across config edits, unlike a slice index.
func (p PortForward) DeviceName(hostPort int) string {
	if s := sanitizeDeviceSuffix(p.Name); s != "" {
		return "port-" + s
	}
	return "port-" + strconv.Itoa(hostPort)
}

// sanitizeDeviceSuffix lowercases s and replaces any character that is not a
// letter, digit, or hyphen with a hyphen. Returns "" for an empty/blank name.
func sanitizeDeviceSuffix(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}

// projectCredential returns the first credential a project file still
// declares (git.credential, tools.<name>.credential), or "".
func projectCredential(cfg ProjectConfig) string {
	declared := func(n *yaml.Node) bool { return n != nil && n.Tag != "!!null" }
	if declared(cfg.Git.Credential) {
		return "git.credential"
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Tools)) {
		if declared(cfg.Tools[name].Credential) {
			return "tools." + name + ".credential"
		}
	}
	return ""
}

// UnmarshalYAML implements custom unmarshaling for DaemonConfig.
// Supports both "rails: bin/rails server" (string) and object form with cmd/autostart/after/ports.
func (d *DaemonConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		d.Cmd = value.Value
		d.Autostart = true
		return nil
	}

	// Object form — need a temporary type to avoid infinite recursion.
	type rawDaemon struct {
		Cmd       string            `yaml:"cmd"`
		Autostart *bool             `yaml:"autostart"`
		After     string            `yaml:"after"`
		Ports     []PortForward     `yaml:"ports"`
		Env       map[string]string `yaml:"env"`
	}
	var raw rawDaemon
	if err := value.Decode(&raw); err != nil {
		return err
	}
	d.Cmd = raw.Cmd
	if raw.Autostart == nil {
		d.Autostart = true
	} else {
		d.Autostart = *raw.Autostart
	}
	d.After = raw.After
	d.Ports = raw.Ports
	d.Env = raw.Env
	return nil
}

// LoadProjectConfig reads .silo.yml (or .silo.yaml) from the given directory.
// If .silo.local.yml (or .silo.local.yaml) exists, its values are merged on
// top, allowing per-machine overrides without modifying the shared config.
// Returns nil with no error if no config file is found.
func LoadProjectConfig(dir string) (*ProjectConfig, error) {
	cfg := loadProjectFile(dir, ".silo.yml", ".silo.yaml")
	local := loadProjectFile(dir, ".silo.local.yml", ".silo.local.yaml")

	if cfg == nil && local == nil {
		return nil, nil
	}

	if cfg != nil && cfg.err != nil {
		return nil, cfg.err
	}
	if local != nil && local.err != nil {
		return nil, local.err
	}

	if cfg == nil {
		return &local.config, nil
	}
	if local == nil {
		return &cfg.config, nil
	}

	merged := mergeProjectConfigs(&cfg.config, &local.config)
	return merged, nil
}

type projectFileResult struct {
	config ProjectConfig
	err    error
}

func loadProjectFile(dir string, names ...string) *projectFileResult {
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return &projectFileResult{err: fmt.Errorf("reading %s: %w", path, err)}
		}

		var cfg ProjectConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return &projectFileResult{err: fmt.Errorf("parsing %s: %w", path, err)}
		}
		if len(cfg.Mounts) > 0 {
			return &projectFileResult{err: fmt.Errorf("parsing %s: project files cannot declare host mounts; move mounts to %s (silo config update does that)", path, GlobalConfigPath())}
		}
		if where := projectCredential(cfg); where != "" {
			return &projectFileResult{err: fmt.Errorf("parsing %s: %s belongs in %s now (silo config update moves it)", path, where, SecretsPath())}
		}
		for agent, agentCfg := range cfg.Agents {
			if agentCfg.Mode != "" {
				if err := ValidateAgentModePath(agent, agentCfg.Mode); err != nil {
					return &projectFileResult{err: fmt.Errorf("parsing %s: %w", path, err)}
				}
			} else if !validPathIdentifier(agent) {
				return &projectFileResult{err: fmt.Errorf("parsing %s: invalid agent name %q", path, agent)}
			}
		}
		return &projectFileResult{config: cfg}
	}
	return nil
}

// mergeProjectConfigs overlays local on top of base. Scalars and the command
// lists (setup, sync, reset, update) are replaced when local sets them. The
// keyed blocks (use, daemons, env, agents, tools, ports) are extended instead:
// local entries are added to the base ones, and a local entry with the same
// name (or, for ports, the same container port) replaces the base entry. So a
// .silo.local.yml only needs to list what it adds or changes.
func mergeProjectConfigs(base, local *ProjectConfig) *ProjectConfig {
	m := *base

	if local.Image != "" {
		m.Image = local.Image
	}
	m.Use = mergeUseLists(base.Use, local.Use)
	if local.Setup != nil {
		m.Setup = local.Setup
	}
	if local.Sync != nil {
		m.Sync = local.Sync
	}
	if local.Reset != nil {
		m.Reset = local.Reset
	}
	if local.Update != nil {
		m.Update = local.Update
	}
	m.Ports = mergePortLists(base.Ports, local.Ports)
	m.Env = mergeMaps(base.Env, local.Env)
	if local.Git.Settings != nil {
		m.Git = local.Git
	}
	m.Agents = mergeMaps(base.Agents, local.Agents)
	if local.Mounts != nil {
		m.Mounts = local.Mounts
	}
	m.Daemons = mergeMaps(base.Daemons, local.Daemons)
	if local.Nesting {
		m.Nesting = local.Nesting
	}

	return &m
}

// mergeMaps returns base extended by local, with local winning on a key
// collision. Neither input is modified. A nil result means both were nil.
func mergeMaps[V any](base, local map[string]V) map[string]V {
	if base == nil && local == nil {
		return nil
	}
	m := make(map[string]V, len(base)+len(local))
	for k, v := range base {
		m[k] = v
	}
	for k, v := range local {
		m[k] = v
	}
	return m
}

// mergeUseLists appends the local presets to the base ones, keeping base order.
// A preset named in both keeps its base position but takes the local params.
func mergeUseLists(base, local UseList) UseList {
	if base == nil && local == nil {
		return nil
	}
	m := make(UseList, 0, len(base)+len(local))
	m = append(m, base...)
	for _, u := range local {
		replaced := false
		for i := range m {
			if m[i].Name == u.Name {
				m[i] = u
				replaced = true
				break
			}
		}
		if !replaced {
			m = append(m, u)
		}
	}
	return m
}

// mergePortLists appends the local port forwards to the base ones. A local
// entry for a container port already forwarded by base replaces that base
// entry, so the host side can be changed per machine without binding the
// container port twice.
func mergePortLists(base, local []PortForward) []PortForward {
	if base == nil && local == nil {
		return nil
	}
	m := make([]PortForward, 0, len(base)+len(local))
	m = append(m, base...)
	for _, pf := range local {
		replaced := false
		if cp, ok := containerPort(pf.Spec); ok {
			for i := range m {
				if bp, ok := containerPort(m[i].Spec); ok && bp == cp {
					m[i] = pf
					replaced = true
					break
				}
			}
		}
		if !replaced {
			m = append(m, pf)
		}
	}
	return m
}

// HTTPRoute is a name under .localhost that the silo service's proxy serves
// for an `http: true` port forward.
type HTTPRoute struct {
	Host          string // e.g. "myapp.localhost"
	Name          string // the forward's name, may be empty
	ContainerPort int
	HostPort      int
}

// HTTPRoutes returns the .localhost names of the project's http: forwards.
// The first one is <project>.localhost; the others get the forward's name
// (or container port) appended: <project>-<name>.localhost. The names have a
// single label in front of the domain, so they also work with one wildcard
// certificate. silo.localhost itself is the silo service's web UI.
func (m *MergedConfig) HTTPRoutes() []HTTPRoute {
	label := strings.ToLower(strings.TrimPrefix(m.ContainerName, "silo-"))
	var routes []HTTPRoute
	for _, p := range m.Ports {
		if !p.HTTP {
			continue
		}
		cp, ok1 := p.ContainerPort()
		hp, ok2 := p.HostPort()
		if !ok1 || !ok2 {
			continue
		}
		host := label
		// silo.localhost is the web UI, so a project called silo starts
		// with the suffixed names.
		if len(routes) > 0 || label == "silo" {
			suffix := sanitizeDeviceSuffix(p.Name)
			if suffix == "" {
				suffix = strconv.Itoa(cp)
			}
			host = label + "-" + suffix
		}
		routes = append(routes, HTTPRoute{Host: host + ".localhost", Name: p.Name, ContainerPort: cp, HostPort: hp})
	}
	return routes
}
