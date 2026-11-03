package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gschlager/silo/internal/config"
)

// The website case: a top-level forward and the daemon declare the same
// container port. The daemon still lists it, with the host port that's really
// forwarded, and only the port marked http: true gets a URL.
func TestStatusJSON(t *testing.T) {
	project := &config.ProjectConfig{
		Ports: []config.PortForward{{Spec: "1313:11313"}, {Spec: "5432:15432"}},
		Daemons: map[string]config.DaemonConfig{
			"hugo": {Cmd: "hugo server", Ports: []config.PortForward{{Spec: "1313", HTTP: true}}},
		},
	}
	cfg := config.Merge(&config.GlobalConfig{Shell: "zsh", User: "dev", DefaultImage: "fedora/43"}, project, "/tmp/website")

	var buf bytes.Buffer
	if err := writeStatusJSON(&buf, cfg, true, true, map[string]string{"hugo": "running"}); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"container": cfg.ContainerName,
		"state":     "running",
		"daemons": []any{map[string]any{
			"name": "hugo", "state": "running", "autostart": false,
			"ports": []any{map[string]any{
				"container": 1313.0, "host": 11313.0, "name": nil, "daemon": "hugo",
				"http": true, "url": "http://localhost:11313/",
			}},
		}},
		"ports": []any{
			map[string]any{
				"container": 1313.0, "host": 11313.0, "name": nil, "daemon": "hugo",
				"http": true, "url": "http://localhost:11313/",
			},
			map[string]any{
				"container": 5432.0, "host": 15432.0, "name": nil, "daemon": nil,
				"http": false, "url": nil,
			},
		},
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Errorf("got  %s\nwant %s", gotJSON, wantJSON)
	}
}
