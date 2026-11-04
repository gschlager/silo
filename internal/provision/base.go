package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gschlager/silo/internal/agents"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	incuscli "github.com/lxc/incus/v7/client"
	"golang.org/x/sys/unix"
)

// A base template is a stopped container per image with everything that
// doesn't depend on the project already done: default_setup, the login shell,
// the dev user and the globally enabled agents. New project containers are
// copied from its snapshot instead of being built from the bare image.

// baseVersion is part of the template hash. Bump it when the base steps change,
// so existing templates are rebuilt.
const baseVersion = "1"

// baseSnapshot is the template snapshot new containers are copied from.
const baseSnapshot = "ready"

// Config keys stored on the template. They're copied along with the template,
// so they're stripped from new containers (all start with BaseMarkerKey).
const (
	baseHashKey   = incus.BaseMarkerKey + ".hash"
	baseShellKey  = incus.BaseMarkerKey + ".shell"
	baseAgentsKey = incus.BaseMarkerKey + ".agents"
	baseImageKey  = incus.BaseMarkerKey + ".image"
	baseBuiltKey  = incus.BaseMarkerKey + ".built"
)

// BaseSpec is everything that goes into a base template. Two projects with the
// same spec share a template.
type BaseSpec struct {
	Image        string
	User         string
	Shell        string
	DefaultSetup []string
	Agents       map[string]config.MergedAgentConfig
}

// BaseSpecFor returns the base template spec for a project config.
func BaseSpecFor(cfg *config.MergedConfig) BaseSpec {
	spec := BaseSpec{
		Image:        cfg.Image,
		User:         cfg.User,
		Shell:        cfg.Shell,
		DefaultSetup: cfg.DefaultSetup,
		Agents:       make(map[string]config.MergedAgentConfig),
	}
	for _, name := range cfg.BaseAgents {
		if a, ok := cfg.Agents[name]; ok {
			a.Enabled = true // a project may disable it; the template still has it
			spec.Agents[name] = a
		}
	}
	return spec
}

// Hash identifies the spec. A template whose stored hash differs is rebuilt.
func (s BaseSpec) Hash() string {
	h := sha256.New()
	field := func(v string) { fmt.Fprintf(h, "%d:%s\n", len(v), v) }
	field(baseVersion)
	field(s.Image)
	field(s.User)
	field(s.Shell)
	for _, c := range s.DefaultSetup {
		field("setup " + c)
	}
	for _, name := range s.agentNames() {
		a := s.Agents[name]
		field("agent " + name)
		for _, d := range a.Deps {
			field("dep " + d)
		}
		field("install " + a.Install)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (s BaseSpec) agentNames() []string {
	names := make([]string, 0, len(s.Agents))
	for name := range s.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// BaseName returns the template container name for an image, e.g.
// "images:fedora/44" -> "silo-base-fedora-44".
func BaseName(image string) string {
	if i := strings.IndexByte(image, ':'); i >= 0 {
		image = image[i+1:]
	}
	var b strings.Builder
	for _, r := range strings.ToLower(image) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	name := "silo-base-" + strings.Trim(b.String(), "-")
	if len(name) > 57 { // leave room for the "-build" suffix (63 max)
		name = name[:57]
	}
	return strings.TrimRight(name, "-")
}

// baseInfo is what a new container inherits from the template it was copied
// from, read from the template's config keys.
type baseInfo struct {
	shell  string
	agents map[string]bool
}

// copyFromBase creates the stopped container `name` from the base template for
// spec, building or rebuilding the template first when needed. The lock is
// held through the copy so a concurrent rebuild can't delete the template
// mid-copy.
func copyFromBase(ctx context.Context, server incuscli.InstanceServer, spec BaseSpec, name string) (baseInfo, error) {
	unlock, err := lockBase()
	if err != nil {
		return baseInfo{}, err
	}
	defer unlock()

	tmpl := BaseName(spec.Image)
	if err := ensureBase(ctx, server, spec, tmpl, false); err != nil {
		return baseInfo{}, err
	}

	status("Creating container %s from base template %s...", name, tmpl)
	if err := incus.CopySnapshot(ctx, server, tmpl, baseSnapshot, name); err != nil {
		return baseInfo{}, err
	}

	inst, err := incus.GetInstance(server, name)
	if err != nil {
		return baseInfo{}, err
	}
	info := baseInfo{shell: inst.Config[baseShellKey], agents: make(map[string]bool)}
	for _, a := range strings.Split(inst.Config[baseAgentsKey], ",") {
		if a != "" {
			info.agents[a] = true
		}
	}
	// Strip the template markers, or the copy would count as a template.
	strip := make(map[string]string)
	for k := range inst.Config {
		if k == incus.BaseMarkerKey || strings.HasPrefix(k, incus.BaseMarkerKey+".") {
			strip[k] = ""
		}
	}
	if err := incus.UpdateConfig(ctx, server, name, strip); err != nil {
		return baseInfo{}, err
	}
	return info, nil
}

// RebuildBase builds the base template for spec from scratch, replacing the
// existing one.
func RebuildBase(ctx context.Context, server incuscli.InstanceServer, spec BaseSpec) error {
	unlock, err := lockBase()
	if err != nil {
		return err
	}
	defer unlock()

	color.StartCapture()
	defer color.StopCapture()
	if err := ensureBase(ctx, server, spec, BaseName(spec.Image), true); err != nil {
		color.DumpCapture()
		return err
	}
	return nil
}

// ensureBase makes sure the template exists and matches spec. A new template is
// built under a temporary name and only swapped in once it's complete, so a
// failed build leaves the previous template in place.
func ensureBase(ctx context.Context, server incuscli.InstanceServer, spec BaseSpec, tmpl string, force bool) error {
	hash := spec.Hash()
	if !force {
		if inst, err := incus.GetInstance(server, tmpl); err == nil {
			if inst.Config[baseHashKey] == hash {
				return nil
			}
			status("Base template %s is out of date, rebuilding...", tmpl)
		}
	}

	build := tmpl + "-build"
	if incus.Exists(server, build) { // leftover from an interrupted build
		incus.Stop(ctx, server, build)
		if err := incus.Delete(ctx, server, build); err != nil {
			return err
		}
	}
	if err := buildBase(ctx, server, spec, build); err != nil {
		return err
	}
	if incus.Exists(server, tmpl) {
		incus.Stop(ctx, server, tmpl)
		if err := incus.Delete(ctx, server, tmpl); err != nil {
			return err
		}
	}
	return incus.Rename(ctx, server, build, tmpl)
}

func buildBase(ctx context.Context, server incuscli.InstanceServer, spec BaseSpec, name string) (err error) {
	status("Building base template from %s (one-time, reused by new containers)...", spec.Image)
	if err := incus.Create(ctx, server, spec.Image, name); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			incus.Stop(context.Background(), server, name)
			incus.Delete(context.Background(), server, name)
		}
	}()

	// Mark it as a template right away, so a half-built one never shows up as
	// a project container.
	if err := incus.UpdateConfig(ctx, server, name, map[string]string{
		incus.BaseMarkerKey: "true",
		"boot.autostart":    "false",
	}); err != nil {
		return err
	}
	if err := startAndWaitForNetwork(ctx, server, name); err != nil {
		return err
	}

	shell, err := runBaseSteps(ctx, server, name, spec)
	if err != nil {
		return err
	}

	// Reset the machine ID so every copy generates its own on first boot.
	// Copies sharing one would also share DHCP client IDs, and with them IPs.
	if _, err := incus.Exec(ctx, server, name, incus.ExecOpts{}, []string{
		"sh", "-c", ": > /etc/machine-id",
	}); err != nil {
		return fmt.Errorf("resetting machine id: %w", err)
	}
	if err := incus.Stop(ctx, server, name); err != nil {
		return err
	}

	if err := incus.UpdateConfig(ctx, server, name, map[string]string{
		baseHashKey:   spec.Hash(),
		baseShellKey:  shell,
		baseAgentsKey: strings.Join(spec.agentNames(), ","),
		baseImageKey:  spec.Image,
		baseBuiltKey:  time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return err
	}
	return incus.CreateSnapshot(ctx, server, name, baseSnapshot)
}

// runBaseSteps does the project-independent part of provisioning in a running
// container and returns the effective login shell.
func runBaseSteps(ctx context.Context, server incuscli.InstanceServer, name string, spec BaseSpec) (string, error) {
	// default_setup runs as root, before user creation so the shell is available.
	if len(spec.DefaultSetup) > 0 {
		status("Running default setup...")
		if err := runCommands(ctx, server, name, incus.ExecOpts{}, spec.DefaultSetup); err != nil {
			return "", fmt.Errorf("default_setup failed: %w", err)
		}
	}

	// Ensure the configured login shell exists (install it if missing, fall
	// back to bash). Decoupled from default_setup so it runs for every image —
	// silo execs every command through this shell.
	shell, err := EnsureShell(ctx, server, name, spec.Shell)
	if err != nil {
		return "", err
	}

	status("Creating user %s...", spec.User)
	if err := CreateUser(ctx, server, name, spec.User, shell); err != nil {
		return "", err
	}

	// Enable prefix history search (Up/Down) for bash line editing.
	if err := configureInputrc(ctx, server, name); err != nil {
		color.Warn("could not configure inputrc: %v", err)
	}

	if len(spec.Agents) > 0 {
		if err := agents.InstallAgents(ctx, server, name, spec.User, shell, spec.Agents); err != nil {
			return "", err
		}
	}
	return shell, nil
}

func startAndWaitForNetwork(ctx context.Context, server incuscli.InstanceServer, name string) error {
	if err := incus.Start(ctx, server, name); err != nil {
		return err
	}
	status("Waiting for network...")
	networkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return incus.WaitForNetwork(networkCtx, server, name, "dns.google")
}

// lockBase serializes template builds and copies across silo processes.
func lockBase() (unlock func(), err error) {
	dir := config.GlobalConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, ".base.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening base template lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("locking base template: %w", err)
	}
	return func() { f.Close() }, nil
}

// BaseTemplate describes an existing template for `silo base list`.
type BaseTemplate struct {
	Name    string
	Image   string
	Hash    string
	Agents  string
	BuiltAt time.Time
}

// ListBases returns the existing base templates.
func ListBases(server incuscli.InstanceServer) ([]BaseTemplate, error) {
	instances, err := server.GetInstances("container")
	if err != nil {
		return nil, fmt.Errorf("listing instances: %w", err)
	}
	var out []BaseTemplate
	for _, inst := range instances {
		if inst.Config[incus.BaseMarkerKey] != "true" {
			continue
		}
		t := BaseTemplate{
			Name:   inst.Name,
			Image:  inst.Config[baseImageKey],
			Hash:   inst.Config[baseHashKey],
			Agents: inst.Config[baseAgentsKey],
		}
		t.BuiltAt, _ = time.Parse(time.RFC3339, inst.Config[baseBuiltKey])
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RemoveBase deletes a base template. Containers already copied from it are
// not affected.
func RemoveBase(ctx context.Context, server incuscli.InstanceServer, name string) error {
	inst, err := incus.GetInstance(server, name)
	if err != nil {
		return err
	}
	if inst.Config[incus.BaseMarkerKey] != "true" {
		return fmt.Errorf("%s is not a base template", name)
	}
	incus.Stop(ctx, server, name)
	return incus.Delete(ctx, server, name)
}
