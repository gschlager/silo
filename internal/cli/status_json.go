package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/svcapi"
)

// statusReport is the `silo status --json` output. It's meant for tools (e.g.
// a panel listing daemons and their links), so fields only get added, never
// renamed. Optional values are null rather than omitted.
type statusReport struct {
	Container string         `json:"container"`
	State     string         `json:"state"` // running | stopped | missing
	Daemons   []daemonReport `json:"daemons"`
	Ports     []portReport   `json:"ports"`
}

type daemonReport struct {
	Name      string       `json:"name"`
	State     string       `json:"state"` // running | failed | stopped
	Autostart bool         `json:"autostart"`
	Ports     []portReport `json:"ports"`
}

type portReport struct {
	Container int     `json:"container"`
	Host      int     `json:"host"`
	Name      *string `json:"name"`
	Daemon    *string `json:"daemon"`
	HTTP      bool    `json:"http"`
	URL       *string `json:"url"` // set when http: true
	// HTTPSURL is the https://<project>.localhost URL, when the silo service's
	// proxy serves this forward.
	HTTPSURL *string `json:"https_url"`
}

// buildStatusReport assembles the report from the merged config and the
// container/daemon state already looked up. A daemon's ports are the forwards
// that actually exist for it, so a daemon port that a top-level ports: entry
// already forwards is listed with that entry's host port.
//
// With the silo service running (proxy not nil), an http: forward's URL is
// its <project>.localhost name.
func buildStatusReport(cfg *config.MergedConfig, exists, running bool, states map[string]string, proxy *svcapi.ProxyInfo) statusReport {
	r := statusReport{
		Container: cfg.ContainerName,
		State:     "missing",
		Daemons:   []daemonReport{},
		Ports:     []portReport{},
	}
	if exists {
		r.State = "stopped"
		if running {
			r.State = "running"
		}
	}

	byDaemon := make(map[string][]portReport)
	for _, p := range cfg.Ports {
		cp, ok1 := p.ContainerPort()
		hp, ok2 := p.HostPort()
		if !ok1 || !ok2 {
			continue // malformed; `silo up` reports these
		}
		pr := portReport{Container: cp, Host: hp, Name: optString(p.Name), Daemon: optString(p.Daemon), HTTP: p.HTTP}
		if p.HTTP {
			pr.URL = optString(fmt.Sprintf("http://localhost:%d/", hp))
			if proxy != nil {
				if host, ok := routeHost(cfg, cp, proxy); ok {
					if u := proxy.URL(host, false); u != "" {
						pr.URL = optString(u)
					}
					pr.HTTPSURL = optString(proxy.URL(host, true))
				}
			}
		}
		r.Ports = append(r.Ports, pr)
		if p.Daemon != "" {
			byDaemon[p.Daemon] = append(byDaemon[p.Daemon], pr)
		}
	}

	for _, name := range sortedDaemonNames(cfg) {
		state := "stopped"
		if s, ok := states[name]; ok {
			state = s
		}
		ports := byDaemon[name]
		if ports == nil {
			ports = []portReport{}
		}
		r.Daemons = append(r.Daemons, daemonReport{
			Name:      name,
			State:     state,
			Autostart: cfg.Daemons[name].Autostart,
			Ports:     ports,
		})
	}
	return r
}

func writeStatusJSON(w io.Writer, cfg *config.MergedConfig, exists, running bool, states map[string]string, proxy *svcapi.ProxyInfo) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(buildStatusReport(cfg, exists, running, states, proxy))
}

// routeHost returns the .localhost name of the http: forward of a container
// port, when the proxy serves it (after `silo up` added its socket device).
func routeHost(cfg *config.MergedConfig, containerPort int, proxy *svcapi.ProxyInfo) (string, bool) {
	for _, r := range proxy.Routes {
		if r.Container == cfg.ContainerName && r.ContainerPort == containerPort {
			return r.Host, true
		}
	}
	return "", false
}

// proxyInfo returns how the silo service's .localhost proxy is set up, or nil
// when the service doesn't run.
func proxyInfo() *svcapi.ProxyInfo {
	c := svcapi.Connect()
	if c == nil {
		return nil
	}
	info, err := c.Proxy()
	if err != nil || (info.HTTPPort == 0 && info.HTTPSPort == 0) {
		return nil
	}
	return &info
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
