package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/gschlager/silo/internal/config"
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
}

// buildStatusReport assembles the report from the merged config and the
// container/daemon state already looked up. A daemon's ports are the forwards
// that actually exist for it, so a daemon port that a top-level ports: entry
// already forwards is listed with that entry's host port.
func buildStatusReport(cfg *config.MergedConfig, exists, running bool, states map[string]string) statusReport {
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

func writeStatusJSON(w io.Writer, cfg *config.MergedConfig, exists, running bool, states map[string]string) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(buildStatusReport(cfg, exists, running, states))
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
