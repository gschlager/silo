package presets

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// rubyPreset installs one or more Ruby versions and wires shell activation.
//
//	use:
//	  ruby:
//	    versions: [3.3, 3.4, "4.0", jruby]   # jruby/truffleruby are just versions
//	    default: "3.4"                        # which version to pin (optional)
//
// MRI versions are managed by rv (https://rv.dev); jruby/truffleruby are
// installed via ruby-install (they need a different toolchain). Activation is
// written to the shell-neutral ~/.silo/env.sh and detects the shell at runtime,
// so it works for bash and zsh and is picked up by daemons.
type rubyPreset struct{}

func (rubyPreset) SetupCommands(params yaml.Node, _ string) ([]string, error) {
	var p struct {
		Versions yaml.Node `yaml:"versions"`
		Default  yaml.Node `yaml:"default"`
	}
	if !params.IsZero() {
		if err := params.Decode(&p); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
	}

	versions := scalarValues(p.Versions)
	if len(versions) == 0 {
		return nil, fmt.Errorf("requires at least one entry in 'versions'")
	}

	var mri, alt []string
	for _, v := range versions {
		if isAltRuby(v) {
			alt = append(alt, v)
		} else {
			mri = append(mri, v)
		}
	}

	// Pin the explicit default, else the last MRI version (rv can't pin jruby/
	// truffleruby, so an alt default just means "don't pin").
	def := p.Default.Value
	if def == "" && len(mri) > 0 {
		def = mri[len(mri)-1]
	}

	var cmds []string

	if len(mri) > 0 {
		cmds = append(cmds,
			`command -v rv >/dev/null 2>&1 || curl -LsSf https://rv.dev/install | sh`,
			`. "$HOME/.cargo/env"`,
		)
		for _, v := range mri {
			cmds = append(cmds, "rv ruby install "+v)
		}
		if def != "" && !isAltRuby(def) {
			cmds = append(cmds, "rv ruby pin "+def)
		}
		// Shell-neutral activation, idempotent across re-provisioning. The rv
		// init line detects the running shell so the same file works for bash
		// and zsh (and for daemons running a login shell).
		rvInit := `if [ -n "$ZSH_VERSION" ]; then eval "$(rv shell init zsh)"; else eval "$(rv shell init bash)"; fi`
		cmds = append(cmds,
			activationGuard(`. "$HOME/.cargo/env"`, "cargo/env"),
			activationGuard(rvInit, "rv shell init"),
			// Activate rv for the rest of this setup session so later commands
			// (bundle install, etc.) see the pinned ruby without needing `rv run`.
			rvInit,
		)
	}

	if len(alt) > 0 {
		cmds = append(cmds,
			`command -v ruby-install >/dev/null 2>&1 || { sudo dnf copr enable -y gschlager/ruby && sudo dnf install -y ruby-install; }`,
			`sudo dnf install -y gcc make libyaml-devel openssl-devel readline-devel zlib-devel`,
		)
		if needsJDK(alt) {
			cmds = append(cmds, `sudo dnf install -y java-latest-openjdk-headless`)
		}
		for _, v := range alt {
			impl, ver := splitAltRuby(v)
			if ver != "" {
				cmds = append(cmds, fmt.Sprintf("ruby-install %s %s", impl, ver))
			} else {
				cmds = append(cmds, "ruby-install "+impl)
			}
		}
	}

	return cmds, nil
}

// isAltRuby reports whether v names an alternative Ruby implementation that rv
// does not manage (jruby, truffleruby), optionally with a version suffix.
func isAltRuby(v string) bool {
	return strings.HasPrefix(v, "jruby") || strings.HasPrefix(v, "truffleruby")
}

// splitAltRuby splits an alt-ruby entry into implementation and optional version:
// "jruby" -> ("jruby", ""), "jruby-9.4" -> ("jruby", "9.4").
func splitAltRuby(v string) (impl, version string) {
	for _, name := range []string{"jruby", "truffleruby"} {
		if v == name {
			return name, ""
		}
		if strings.HasPrefix(v, name+"-") {
			return name, strings.TrimPrefix(v, name+"-")
		}
	}
	return v, ""
}

// needsJDK reports whether any requested alt ruby needs a JDK (jruby does).
func needsJDK(alt []string) bool {
	for _, v := range alt {
		if strings.HasPrefix(v, "jruby") {
			return true
		}
	}
	return false
}
