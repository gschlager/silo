package provision

import "testing"

func TestParsePortSpec(t *testing.T) {
	tests := []struct {
		spec          string
		containerPort int
		hostPort      int
		wantErr       bool
	}{
		{"3000:13000", 3000, 13000, false},
		{"80:8080", 80, 8080, false},
		{"3000", 3000, 3000, false},
		{"443", 443, 443, false},
		{"0:80", 0, 0, true},
		{"80:0", 0, 0, true},
		{"99999:80", 0, 0, true},
		{"abc:80", 0, 0, true},
		{"80:abc", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			cp, hp, err := parsePortSpec(tt.spec)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for %q", tt.spec)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.spec, err)
			}
			if cp != tt.containerPort || hp != tt.hostPort {
				t.Errorf("parsePortSpec(%q) = %d, %d; want %d, %d", tt.spec, cp, hp, tt.containerPort, tt.hostPort)
			}
		})
	}
}

func TestParseMountSpec(t *testing.T) {
	tests := []struct {
		spec     string
		source   string
		target   string
		readonly bool
		wantErr  bool
	}{
		{"/host/path:/container/path", "/host/path", "/container/path", false, false},
		{"/host:/container:ro", "/host", "/container", true, false},
		{"/host:/container:rw", "/host", "/container", false, false},
		{"nocolon", "", "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			source, target, readonly, err := parseMountSpec(tt.spec)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for %q", tt.spec)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if source != tt.source || target != tt.target || readonly != tt.readonly {
				t.Errorf("parseMountSpec(%q) = %q, %q, %v; want %q, %q, %v",
					tt.spec, source, target, readonly, tt.source, tt.target, tt.readonly)
			}
		})
	}
}

func TestShellEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"hello world", "'hello world'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
		{`"quoted"`, `'"quoted"'`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := shellEscape(tt.input)
			if got != tt.want {
				t.Errorf("shellEscape(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPathPrependLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "prepend with $PATH expands and dedup-guards on first segment",
			input: "/opt/android-sdk/cmdline-tools/latest/bin:/opt/android-sdk/platform-tools:$PATH",
			want:  `case ":$PATH:" in *":/opt/android-sdk/cmdline-tools/latest/bin:"*) ;; *) export PATH="/opt/android-sdk/cmdline-tools/latest/bin:/opt/android-sdk/platform-tools:$PATH" ;; esac`,
		},
		{
			name:  "single dir prepend",
			input: "/opt/bin:$PATH",
			want:  `case ":$PATH:" in *":/opt/bin:"*) ;; *) export PATH="/opt/bin:$PATH" ;; esac`,
		},
		{
			name:  "absolute path without reference still guards",
			input: "/usr/bin:/bin",
			want:  `case ":$PATH:" in *":/usr/bin:"*) ;; *) export PATH="/usr/bin:/bin" ;; esac`,
		},
		{
			name:  "leading variable reference skips the guard",
			input: "$HOME/bin:$PATH",
			want:  `export PATH="$HOME/bin:$PATH"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pathPrependLine(tt.input); got != tt.want {
				t.Errorf("pathPrependLine(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}
