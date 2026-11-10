package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/gschlager/silo/internal/agents"
	"github.com/gschlager/silo/internal/color"
	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/svcapi"
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

// baseMaxAge is how old a template may get before it's rebuilt in the
// background. Rebuilding (rather than updating in place) picks up the current
// upstream image, packages from default_setup and the latest agents.
const baseMaxAge = 7 * 24 * time.Hour

// baseRetryAfter is how long silo waits before trying another background
// rebuild after one failed.
const baseRetryAfter = 24 * time.Hour

// copyFromBase creates the stopped container `name` from the base template for
// spec. A missing or outdated template is built first, which the caller waits
// for. A template that's merely old is used as is and rebuilt in the
// background for next time. With fresh, the template is always rebuilt first.
func copyFromBase(ctx context.Context, server incuscli.InstanceServer, spec BaseSpec, name string, fresh bool) (baseInfo, error) {
	tmpl := BaseName(spec.Image)
	if fresh {
		if err := buildBaseLocked(ctx, server, spec, tmpl, buildAlways); err != nil {
			return baseInfo{}, err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		info, copied, builtAt, err := copyIfCurrent(ctx, server, spec, tmpl, name)
		if err != nil {
			return baseInfo{}, err
		}
		if copied {
			if age := time.Since(builtAt); !builtAt.IsZero() && age > baseMaxAge {
				startBackgroundRefresh(spec.Image, tmpl, age)
			}
			return info, nil
		}
		if err := buildBaseLocked(ctx, server, spec, tmpl, buildIfOutdated); err != nil {
			return baseInfo{}, err
		}
	}
	return baseInfo{}, fmt.Errorf("base template %s is still outdated after rebuilding it", tmpl)
}

// copyIfCurrent copies the template if it exists and matches spec. It holds the
// swap lock so a rebuild can't replace the template mid-copy.
func copyIfCurrent(ctx context.Context, server incuscli.InstanceServer, spec BaseSpec, tmpl, name string) (info baseInfo, copied bool, builtAt time.Time, err error) {
	unlock, err := lockFile(".base.lock", true)
	if err != nil {
		return info, false, builtAt, err
	}
	defer unlock()

	t, err := incus.GetInstance(server, tmpl)
	if err != nil || t.Config[baseHashKey] != spec.Hash() {
		return info, false, builtAt, nil
	}
	builtAt, _ = time.Parse(time.RFC3339, t.Config[baseBuiltKey])

	status("Creating container %s from base template %s...", name, tmpl)
	if err := incus.CopySnapshot(ctx, server, tmpl, baseSnapshot, name); err != nil {
		return info, false, builtAt, err
	}

	inst, err := incus.GetInstance(server, name)
	if err != nil {
		return info, false, builtAt, err
	}
	info = baseInfo{shell: inst.Config[baseShellKey], agents: make(map[string]bool)}
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
		return info, false, builtAt, err
	}
	return info, true, builtAt, nil
}

type buildMode int

const (
	buildIfOutdated buildMode = iota // missing or hash differs
	buildIfOld                       // ... or older than baseMaxAge
	buildAlways
)

// buildBaseLocked rebuilds the template under the build lock, so only one
// build runs at a time. The new template is built under a temporary name and
// only swapped in (under the swap lock) once complete, so copies can continue
// during a build and a failed build leaves the previous template in place.
func buildBaseLocked(ctx context.Context, server incuscli.InstanceServer, spec BaseSpec, tmpl string, mode buildMode) error {
	unlock, err := lockFile(".base-build.lock", true)
	if err != nil {
		return err
	}
	defer unlock()
	_, err = buildBaseHoldingLock(ctx, server, spec, tmpl, mode)
	return err
}

// buildBaseHoldingLock does the work of buildBaseLocked; the caller holds the
// build lock. It reports whether it built a new template.
func buildBaseHoldingLock(ctx context.Context, server incuscli.InstanceServer, spec BaseSpec, tmpl string, mode buildMode) (bool, error) {
	// Check again: another process may have built it while we waited.
	if mode != buildAlways {
		if t, err := incus.GetInstance(server, tmpl); err == nil && t.Config[baseHashKey] == spec.Hash() {
			builtAt, _ := time.Parse(time.RFC3339, t.Config[baseBuiltKey])
			if mode == buildIfOutdated || time.Since(builtAt) <= baseMaxAge {
				return false, nil
			}
		} else if err == nil {
			status("Base template %s is out of date, rebuilding...", tmpl)
		}
	}

	build := tmpl + "-build"
	if incus.Exists(server, build) { // leftover from an interrupted build
		incus.Stop(ctx, server, build)
		if err := incus.Delete(ctx, server, build); err != nil {
			return false, err
		}
	}
	if err := buildBase(ctx, server, spec, build); err != nil {
		return false, err
	}

	unlock, err := lockFile(".base.lock", true)
	if err != nil {
		return false, err
	}
	defer unlock()
	if incus.Exists(server, tmpl) {
		incus.Stop(ctx, server, tmpl)
		if err := incus.Delete(ctx, server, tmpl); err != nil {
			return false, err
		}
	}
	if err := incus.Rename(ctx, server, build, tmpl); err != nil {
		return false, err
	}
	os.Remove(refreshFailedPath(tmpl))
	return true, nil
}

// RebuildBase builds the base template for spec from scratch, replacing the
// existing one.
func RebuildBase(ctx context.Context, server incuscli.InstanceServer, spec BaseSpec) error {
	color.StartCapture()
	defer color.StopCapture()
	if err := buildBaseLocked(ctx, server, spec, BaseName(spec.Image), buildAlways); err != nil {
		color.DumpCapture()
		return err
	}
	return nil
}

// RefreshBase rebuilds the template if it's older than baseMaxAge. It's what
// the background process started by startBackgroundRefresh runs. It does
// nothing if another build is already running. A failure is recorded, so the
// next `silo up` reports it and doesn't retry right away.
func RefreshBase(ctx context.Context, server incuscli.InstanceServer, spec BaseSpec) error {
	tmpl := BaseName(spec.Image)
	unlock, err := lockFile(".base-build.lock", false)
	if err != nil {
		return err
	}
	if unlock == nil {
		fmt.Println("Another base template build is running; nothing to do.")
		return nil
	}
	defer unlock()

	built, err := buildBaseHoldingLock(ctx, server, spec, tmpl, buildIfOld)
	if err != nil {
		os.WriteFile(refreshFailedPath(tmpl), []byte(err.Error()+"\n"), 0600)
		return err
	}
	if built {
		color.Success("Base template %s is ready.", tmpl)
	} else {
		fmt.Printf("Base template %s is up to date; nothing to do.\n", tmpl)
	}
	return nil
}

// startBackgroundRefresh has the silo service rebuild the template for the
// image, or starts a detached `silo base refresh` when the service doesn't
// run, unless a recent background refresh failed. Output goes to a log file.
func startBackgroundRefresh(image, tmpl string, age time.Duration) {
	if RefreshRecentlyFailed(tmpl) {
		color.Warn("The last background rebuild of base template %s failed; see %s or run `silo base rebuild`.", tmpl, RefreshLogPath())
		return
	}
	if c := svcapi.Connect(); c != nil {
		if err := c.RefreshBase(image); err == nil {
			status("Base template %s is %d days old; the silo service rebuilds it in the background for the next container.", tmpl, int(age.Hours()/24))
			return
		}
	}
	if err := StartRefreshProcess(image, false); err != nil {
		color.Warn("could not start the base template refresh: %v", err)
		return
	}
	status("Base template %s is %d days old; rebuilding it in the background for the next container.", tmpl, int(age.Hours()/24))
}

// RefreshRecentlyFailed reports whether the last background refresh of the
// template failed less than a day ago, so it isn't retried right away.
func RefreshRecentlyFailed(tmpl string) bool {
	fi, err := os.Stat(refreshFailedPath(tmpl))
	return err == nil && time.Since(fi.ModTime()) < baseRetryAfter
}

// BaseMaxAge is the age after which a template is rebuilt in the background.
const BaseMaxAge = baseMaxAge

// StartRefreshProcess runs `silo base refresh --image <image>`, writing to
// the refresh log. Detached, it starts the process in its own session so it
// outlives the terminal, and returns right away; otherwise (the silo service)
// it waits for the process.
func StartRefreshProcess(image string, wait bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	log, err := os.OpenFile(RefreshLogPath(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "base", "refresh", "--image", image)
	cmd.Stdout = log
	cmd.Stderr = log
	if !wait {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	err = cmd.Start()
	log.Close() // the child has its own copy
	if err != nil {
		return err
	}
	if wait {
		return cmd.Wait()
	}
	cmd.Process.Release()
	return nil
}

// RefreshLogPath is where the background template refresh writes its output.
func RefreshLogPath() string {
	return filepath.Join(config.GlobalConfigDir(), "base-refresh.log")
}

func refreshFailedPath(tmpl string) string {
	return filepath.Join(config.GlobalConfigDir(), "."+tmpl+".refresh-failed")
}

func buildBase(ctx context.Context, server incuscli.InstanceServer, spec BaseSpec, name string) (err error) {
	status("Building base template from %s (reused by new containers)...", spec.Image)
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

	status("Saving base template...")
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
	MarkStarting(name)
	if err := incus.Start(ctx, server, name); err != nil {
		return err
	}
	status("Waiting for network...")
	networkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return incus.WaitForNetwork(networkCtx, server, name, "dns.google")
}

// lockFile takes an exclusive lock on a file in the silo config dir. With
// wait false it returns a nil unlock func when the lock is already held.
func lockFile(name string, wait bool) (unlock func(), err error) {
	dir := config.GlobalConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening lock %s: %w", name, err)
	}
	how := unix.LOCK_EX
	if !wait {
		how |= unix.LOCK_NB
	}
	if err := unix.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if !wait && err == unix.EWOULDBLOCK {
			return nil, nil
		}
		return nil, fmt.Errorf("locking %s: %w", name, err)
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
