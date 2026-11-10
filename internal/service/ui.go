package service

import (
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/configupdate"
	"github.com/gschlager/silo/internal/incus"
	"github.com/gschlager/silo/internal/provision"
	"github.com/gschlager/silo/internal/svcapi"
	incuscli "github.com/lxc/incus/v7/client"
	"golang.org/x/crypto/ssh"
)

// The web UI at http(s)://silo.localhost, served by the proxy. It shows the
// silo containers with their URLs, daemons and snapshots, and can start,
// stop and restart them.
//
// Who can use it:
//   - only processes running as this user: the proxy ports are on loopback,
//     where other users of the machine could reach them too, so every
//     request's client socket is looked up in /proc/net/tcp (tcpPeerUID)
//   - only the UI itself: API requests must be same-origin (Sec-Fetch-Site,
//     Origin), and changes also need the X-Silo-UI header, which a page on
//     another origin can't send without a CORS preflight the UI never
//     answers. Apps in containers run on <project>.localhost, which counts as
//     the same site but not the same origin, so they can't use the API either.
//   - it can't be framed (CSP frame-ancestors 'none'), and everything that
//     comes from a container (names, logs) is rendered as text.

// UIHost is the name of the web UI.
const UIHost = "silo.localhost"

//go:embed ui
var uiFiles embed.FS

type connKey struct{}

// connContext remembers each proxy connection, so the UI can check who's on
// the other end.
func connContext(ctx context.Context, c net.Conn) context.Context {
	if tc, ok := c.(*tls.Conn); ok {
		c = tc.NetConn()
	}
	return context.WithValue(ctx, connKey{}, c)
}

// peerIsMe is replaced in tests, where requests don't come in over TCP.
var peerIsMe = func(r *http.Request) bool {
	c, ok := r.Context().Value(connKey{}).(net.Conn)
	if !ok {
		return false
	}
	uid, err := tcpPeerUID(c)
	return err == nil && uid == os.Getuid()
}

func (s *Server) uiHandler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(uiFiles, "ui")
	mux.Handle("GET /", http.FileServerFS(static))

	api := func(pattern string, fn func(w http.ResponseWriter, r *http.Request) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			out, err := fn(w, r)
			w.Header().Set("Content-Type", "application/json")
			if err != nil {
				status := http.StatusInternalServerError
				if he, ok := err.(httpError); ok {
					status = he.status
				}
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(out)
		})
	}

	api("GET /api/overview", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.uiOverview(r.Context()), nil
	})
	api("POST /api/containers/{name}/{action}", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return nil, s.uiContainerAction(r.Context(), r.PathValue("name"), r.PathValue("action"))
	})
	api("POST /api/containers/{name}/daemons/{daemon}/{action}", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return nil, s.uiDaemonAction(r.Context(), r.PathValue("name"), r.PathValue("daemon"), r.PathValue("action"))
	})
	api("GET /api/containers/{name}/daemons/{daemon}/logs", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.uiDaemonLogs(r.Context(), r.PathValue("name"), r.PathValue("daemon"))
	})
	api("GET /api/containers/{name}/snapshots", func(w http.ResponseWriter, r *http.Request) (any, error) {
		srv, err := s.uiContainer(r.PathValue("name"))
		if err != nil {
			return nil, err
		}
		snaps, err := incus.ListSnapshots(srv, r.PathValue("name"))
		if snaps == nil {
			snaps = []incus.SnapshotInfo{}
		}
		return snaps, err
	})
	api("POST /api/containers/{name}/snapshots", func(w http.ResponseWriter, r *http.Request) (any, error) {
		srv, err := s.uiContainer(r.PathValue("name"))
		if err != nil {
			return nil, err
		}
		snap := "ui-" + time.Now().Format("20060102-150405")
		return map[string]string{"name": snap}, incus.CreateSnapshot(r.Context(), srv, r.PathValue("name"), snap)
	})
	api("POST /api/containers/{name}/snapshots/{snap}/restore", func(w http.ResponseWriter, r *http.Request) (any, error) {
		srv, err := s.uiContainer(r.PathValue("name"))
		if err != nil {
			return nil, err
		}
		log.Printf("%s: restoring snapshot %s (web UI)", r.PathValue("name"), r.PathValue("snap"))
		return nil, incus.RestoreSnapshot(r.Context(), srv, r.PathValue("name"), r.PathValue("snap"))
	})
	api("GET /api/containers/{name}/config-updates", func(w http.ResponseWriter, r *http.Request) (any, error) {
		dir, err := s.uiProjectDir(r.PathValue("name"))
		if err != nil {
			return nil, err
		}
		ss, err := configupdate.Check(dir, projectLabel(r.PathValue("name")))
		out := []uiConfigUpdateDetail{}
		for _, sg := range ss {
			d := uiConfigUpdateDetail{uiConfigUpdate: uiConfigUpdate{Title: sg.Title, Careful: sg.Careful}, Detail: sg.Detail,
				Files: []uiFileDiff{{Path: relTo(dir, sg.File), Lines: diffLines(sg.Before, sg.After)}}}
			for _, o := range sg.Others {
				d.Files = append(d.Files, uiFileDiff{Path: o.Path, Lines: diffLines(o.Before, o.After)})
			}
			out = append(out, d)
		}
		return out, err
	})
	api("POST /api/containers/{name}/config-updates", func(w http.ResponseWriter, r *http.Request) (any, error) {
		dir, err := s.uiProjectDir(r.PathValue("name"))
		if err != nil {
			return nil, err
		}
		return s.uiApplyConfigUpdates(r.Context(), r.PathValue("name"), dir)
	})
	api("POST /api/config-updates", func(w http.ResponseWriter, r *http.Request) (any, error) {
		srv := s.incus()
		if srv == nil {
			return nil, errStatus(http.StatusServiceUnavailable, "not connected to Incus")
		}
		insts, err := incus.ListSiloInstances(srv)
		if err != nil {
			return nil, err
		}
		var total uiApplied
		for _, inst := range insts {
			dir := inst.Devices["workspace"]["source"]
			if inst.Config[incus.TmpMarkerKey] == "true" || dir == "" {
				continue
			}
			res, err := s.uiApplyConfigUpdates(r.Context(), inst.Name, dir)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", projectLabel(inst.Name), err)
			}
			total.Applied += res.Applied
			total.Left += res.Left
			if res.Applied > 0 {
				total.Projects++
			}
		}
		return total, nil
	})
	api("POST /api/secrets/forget", func(w http.ResponseWriter, r *http.Request) (any, error) {
		n := s.secrets.forget("asked to in the web UI")
		s.publish(Event{Type: "secrets-forgotten", Message: "web UI"})
		return svcapi.ForgetResult{Count: n}, nil
	})
	api("POST /api/bases/refresh", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in svcapi.BaseRefresh
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || in.Image == "" {
			return nil, errStatus(http.StatusBadRequest, "missing image")
		}
		return map[string]bool{"started": s.base.refresh(in.Image)}, nil
	})
	mux.HandleFunc("GET /api/events", s.uiEvents)

	return s.uiGuard(mux)
}

// uiGuard enforces who may use the UI (see the comment at the top).
func (s *Server) uiGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")

		if !peerIsMe(r) {
			http.Error(w, "silo's web UI only answers your own user", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && !uiOrigin(origin) {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
			if r.Method != http.MethodGet && r.Header.Get("X-Silo-UI") != "1" {
				http.Error(w, "missing X-Silo-UI header", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// uiOrigin reports whether an Origin header is the UI's own.
func uiOrigin(origin string) bool {
	for _, scheme := range []string{"http://", "https://"} {
		if rest, ok := strings.CutPrefix(origin, scheme); ok {
			host, _, err := net.SplitHostPort(rest)
			if err != nil {
				host = rest
			}
			return host == UIHost
		}
	}
	return false
}

// uiContainer checks that name is a silo container (not a base template) and
// returns the Incus connection.
func (s *Server) uiContainer(name string) (incuscli.InstanceServer, error) {
	srv := s.incus()
	if srv == nil {
		return nil, errStatus(http.StatusServiceUnavailable, "not connected to Incus")
	}
	insts, err := incus.ListSiloInstances(srv)
	if err != nil {
		return nil, err
	}
	for _, inst := range insts {
		if inst.Name == name {
			return srv, nil
		}
	}
	return nil, errStatus(http.StatusNotFound, "no silo container %q", name)
}

// uiProjectDir returns the project directory of a silo container (not a tmp
// one: its directory belongs to another project, or it has none).
func (s *Server) uiProjectDir(name string) (string, error) {
	srv, err := s.uiContainer(name)
	if err != nil {
		return "", err
	}
	inst, _, err := srv.GetInstance(name)
	if err != nil {
		return "", err
	}
	dir := inst.Devices["workspace"]["source"]
	if inst.Config[incus.TmpMarkerKey] == "true" || dir == "" {
		return "", errStatus(http.StatusNotFound, "%s has no project config", projectLabel(name))
	}
	return dir, nil
}

// uiApplied is what applying config updates did.
type uiApplied struct {
	Applied  int `json:"applied"`
	Left     int `json:"left"` // careful ones, for the terminal
	Projects int `json:"projects,omitempty"`
}

// configUpdateMu keeps two applies from editing the same files at once.
var configUpdateMu sync.Mutex

func (s *Server) uiApplyConfigUpdates(ctx context.Context, name, dir string) (uiApplied, error) {
	configUpdateMu.Lock()
	defer configUpdateMu.Unlock()
	n, left, err := configupdate.ApplyAll(dir, projectLabel(name))
	if n > 0 {
		log.Printf("%s: applied %d config updates (web UI)", name, n)
		s.publish(Event{Type: "config-updated", Container: name, Message: fmt.Sprintf("%d changes", n)})
		s.uiSyncPorts(ctx, name)
	}
	return uiApplied{Applied: n, Left: len(left)}, err
}

// uiSyncPorts gives the container the port devices its config asks for, like
// silo up does, so an http: port marked a moment ago gets its .localhost
// name (the proxy only serves ports with a socket device).
func (s *Server) uiSyncPorts(ctx context.Context, name string) {
	srv := s.incus()
	cfg, err := s.containerConfig(name)
	if srv == nil || err != nil {
		return
	}
	if err := provision.ReconcilePorts(ctx, srv, name, cfg.Ports); err != nil {
		log.Printf("%s: syncing port forwards: %v", name, err)
		return
	}
	s.proxy.rebuild()
}

func diffLines(before, after string) []uiDiffLine {
	out := []uiDiffLine{}
	for _, l := range configupdate.Diff(before, after, 2) {
		op := string(l.Op)
		if l.Op == 0 {
			op = ""
		}
		out = append(out, uiDiffLine{Op: op, Text: l.Text})
	}
	return out
}

func relTo(dir, path string) string {
	if r, err := filepath.Rel(dir, path); err == nil {
		return r
	}
	return path
}

func (s *Server) uiContainerAction(ctx context.Context, name, action string) error {
	srv, err := s.uiContainer(name)
	if err != nil {
		return err
	}
	log.Printf("%s: %s (web UI)", name, action)
	switch action {
	case "start":
		// Like silo up: port devices added while it's stopped bind cleanly.
		s.uiSyncPorts(ctx, name)
		return incus.Start(ctx, srv, name)
	case "stop":
		return incus.Stop(ctx, srv, name)
	case "restart":
		return incus.Restart(ctx, srv, name)
	}
	return errStatus(http.StatusNotFound, "unknown action %q", action)
}

func (s *Server) uiDaemonAction(ctx context.Context, name, daemon, action string) error {
	srv, err := s.uiContainer(name)
	if err != nil {
		return err
	}
	if action != "start" && action != "stop" && action != "restart" {
		return errStatus(http.StatusNotFound, "unknown action %q", action)
	}
	cfg, err := s.containerConfig(name)
	if err != nil {
		return err
	}
	if _, ok := cfg.Daemons[daemon]; !ok {
		return errStatus(http.StatusNotFound, "no daemon %q in %s", daemon, name)
	}
	log.Printf("%s: %s daemon %s (web UI)", name, action, daemon)
	return provision.ControlDaemon(ctx, srv, cfg, action, daemon)
}

func (s *Server) uiDaemonLogs(ctx context.Context, name, daemon string) (any, error) {
	srv, err := s.uiContainer(name)
	if err != nil {
		return nil, err
	}
	cfg, err := s.containerConfig(name)
	if err != nil {
		return nil, err
	}
	if _, ok := cfg.Daemons[daemon]; !ok {
		return nil, errStatus(http.StatusNotFound, "no daemon %q in %s", daemon, name)
	}
	if !incus.IsRunning(srv, name) {
		return map[string]string{"text": "The container isn't running."}, nil
	}
	text, err := provision.DaemonLogs(ctx, srv, name, cfg.User, daemon, 300)
	return map[string]string{"text": text}, err
}

// uiEvents streams events to the browser as server-sent events.
func (s *Server) uiEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	ch, cancel := s.events.subscribe()
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stop:
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case e := <-ch:
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// Overview types, as the UI's JavaScript reads them.

type uiOverview struct {
	Service    svcapi.StatusInfo `json:"service"`
	Proxy      svcapi.ProxyInfo  `json:"proxy"`
	IdleStop   string            `json:"idle_stop"`
	Signing    string            `json:"signing"` // key fingerprint, or ""
	Containers []uiContainer     `json:"containers"`
	Bases      []uiBase          `json:"bases"`
}

type uiContainer struct {
	Name        string     `json:"name"`
	Label       string     `json:"label"`
	ProjectDir  string     `json:"project_dir"`
	State       string     `json:"state"` // running | stopped | ...
	Tmp         bool       `json:"tmp"`
	IPv4        string     `json:"ipv4"`
	MemoryBytes int64      `json:"memory_bytes"`
	LastActive  *time.Time `json:"last_active"`
	URLs        []uiURL    `json:"urls"`
	Ports       []uiPort   `json:"ports"`
	Daemons     []uiDaemon `json:"daemons"`
	Snapshots   int        `json:"snapshots"`
	ConfigError string     `json:"config_error,omitempty"`
	// ConfigUpdates are what silo config update suggests for the project.
	ConfigUpdates []uiConfigUpdate `json:"config_updates"`
	// Outdated is set when an older silo built the container (or one from
	// before silo recorded it): rebuilding it gets the current provisioning.
	Outdated bool   `json:"outdated"`
	BuiltBy  string `json:"built_by"` // silo release, if recorded
}

type uiConfigUpdate struct {
	Title   string `json:"title"`
	Careful bool   `json:"careful"` // not applied from the UI
}

type uiConfigUpdateDetail struct {
	uiConfigUpdate
	Detail string       `json:"detail"`
	Files  []uiFileDiff `json:"files"`
}

type uiFileDiff struct {
	Path  string       `json:"path"`
	Lines []uiDiffLine `json:"lines"`
}

// uiDiffLine is a diff line; Op is " ", "-", "+", or "" for skipped lines.
type uiDiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

type uiURL struct {
	Host     string `json:"host"`
	URL      string `json:"url"`
	HTTPSURL string `json:"https_url"`
	Port     int    `json:"container_port"`
}

type uiPort struct {
	Container int    `json:"container"`
	Host      int    `json:"host"`
	Name      string `json:"name"`
	HTTP      bool   `json:"http"`
}

type uiDaemon struct {
	Name      string `json:"name"`
	Cmd       string `json:"cmd"`
	State     string `json:"state"` // running | failed | stopped
	Autostart bool   `json:"autostart"`
}

type uiBase struct {
	Name    string    `json:"name"`
	Image   string    `json:"image"`
	BuiltAt time.Time `json:"built_at"`
	Running bool      `json:"refreshing"`
}

func (s *Server) uiOverview(ctx context.Context) uiOverview {
	o := uiOverview{Service: s.status(), Proxy: s.proxy.info(), Containers: []uiContainer{}, Bases: []uiBase{}}
	if g, err := config.LoadGlobalConfig(); err == nil {
		if d := g.IdleStopAfter(); d > 0 {
			o.IdleStop = formatIdle(d)
		}
		if key := g.SigningKey(); key != "" {
			if pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key)); err == nil {
				o.Signing = ssh.FingerprintSHA256(pub)
			}
		}
	}
	srv := s.incus()
	if srv == nil {
		return o
	}
	insts, err := incus.ListSiloInstances(srv)
	if err != nil {
		return o
	}

	o.Containers = make([]uiContainer, len(insts))
	var wg sync.WaitGroup
	for i, inst := range insts {
		c := uiContainer{
			Name:       inst.Name,
			Label:      projectLabel(inst.Name),
			ProjectDir: inst.Devices["workspace"]["source"],
			State:      strings.ToLower(inst.Status),
			Tmp:        inst.Config[incus.TmpMarkerKey] == "true",
			URLs:       []uiURL{},
			Ports:      []uiPort{},
			Daemons:    []uiDaemon{},
			Outdated:   inst.Config[incus.ProvisionKey] != strconv.Itoa(provision.ContainerVersion),
			BuiltBy:    inst.Config[incus.ProvisionByKey],

			ConfigUpdates: []uiConfigUpdate{},
		}
		if c.ProjectDir == "" {
			c.ProjectDir = inst.Config["user.silo.tmp.dir"]
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.fillContainer(ctx, srv, &c, o.Proxy)
			o.Containers[i] = c
		}()
	}
	wg.Wait()
	slices.SortFunc(o.Containers, func(a, b uiContainer) int { return strings.Compare(a.Label, b.Label) })

	if bases, err := provision.ListBases(srv); err == nil {
		refreshing := s.base.images()
		for _, b := range bases {
			o.Bases = append(o.Bases, uiBase{Name: b.Name, Image: b.Image, BuiltAt: b.BuiltAt, Running: slices.Contains(refreshing, b.Image)})
		}
	}
	return o
}

// fillContainer adds what needs the config or an exec to a container.
func (s *Server) fillContainer(ctx context.Context, srv incuscli.InstanceServer, c *uiContainer, proxy svcapi.ProxyInfo) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c.Snapshots = incus.SnapshotCount(srv, c.Name)
	if t := s.idle.lastActiveAt(c.Name); !t.IsZero() {
		c.LastActive = &t
	}
	running := c.State == "running"
	if running {
		if st, err := incus.GetInstanceState(srv, c.Name); err == nil {
			c.MemoryBytes = st.Memory.Usage
			for _, a := range st.Network["eth0"].Addresses {
				if a.Family == "inet" {
					c.IPv4 = a.Address
				}
			}
		}
	}

	if !c.Tmp && c.ProjectDir != "" {
		if ss, err := configupdate.Check(c.ProjectDir, c.Label); err == nil {
			for _, sg := range ss {
				c.ConfigUpdates = append(c.ConfigUpdates, uiConfigUpdate{Title: sg.Title, Careful: sg.Careful})
			}
		}
	}

	cfg, err := s.containerConfig(c.Name)
	if err != nil {
		c.ConfigError = err.Error()
		return
	}
	for _, r := range proxy.Routes {
		if r.Container == c.Name {
			c.URLs = append(c.URLs, uiURL{Host: r.Host, URL: proxy.URL(r.Host, false), HTTPSURL: proxy.URL(r.Host, true), Port: r.ContainerPort})
		}
	}
	for _, p := range cfg.Ports {
		cp, ok1 := p.ContainerPort()
		hp, ok2 := p.HostPort()
		if ok1 && ok2 {
			c.Ports = append(c.Ports, uiPort{Container: cp, Host: hp, Name: p.Name, HTTP: p.HTTP})
		}
	}
	names := make([]string, 0, len(cfg.Daemons))
	for name := range cfg.Daemons {
		names = append(names, name)
	}
	slices.Sort(names)
	states := map[string]string{}
	if running && len(names) > 0 {
		states = provision.DaemonStates(ctx, srv, c.Name, cfg.User, names)
	}
	for _, name := range names {
		state := states[name]
		if state == "" {
			state = "stopped"
		}
		c.Daemons = append(c.Daemons, uiDaemon{Name: name, Cmd: cfg.Daemons[name].Cmd, State: state, Autostart: cfg.Daemons[name].Autostart})
	}
}
