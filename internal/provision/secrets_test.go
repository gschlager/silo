package provision

import (
	"strings"
	"testing"
)

func TestResolveSecretsInto(t *testing.T) {
	t.Run("literals are used as-is", func(t *testing.T) {
		env := map[string]string{}
		if err := resolveSecretsInto(env, map[string]string{"TOKEN": "plain"}, "secret"); err != nil {
			t.Fatalf("resolveSecretsInto: %v", err)
		}
		if env["TOKEN"] != "plain" {
			t.Errorf("expected 'plain', got %q", env["TOKEN"])
		}
	})

	t.Run("the reserved github name wires both token variables", func(t *testing.T) {
		env := map[string]string{}
		if err := resolveSecretsInto(env, map[string]string{"github": "ghp_x"}, "secret"); err != nil {
			t.Fatalf("resolveSecretsInto: %v", err)
		}
		if env["GITHUB_TOKEN"] != "ghp_x" || env["GH_TOKEN"] != "ghp_x" {
			t.Errorf("expected GITHUB_TOKEN and GH_TOKEN, got %v", env)
		}
		if _, ok := env["github"]; ok {
			t.Error("the reserved name should not be exported verbatim")
		}
	})

	// ResolveSessionEnv applies preset secrets first, then the project's own, so
	// a project can override a reference it inherited from a preset.
	t.Run("later calls override earlier ones", func(t *testing.T) {
		env := map[string]string{}
		if err := resolveSecretsInto(env, map[string]string{"TOKEN": "from-preset"}, "preset secret"); err != nil {
			t.Fatalf("resolveSecretsInto: %v", err)
		}
		if err := resolveSecretsInto(env, map[string]string{"TOKEN": "from-project"}, "secret"); err != nil {
			t.Fatalf("resolveSecretsInto: %v", err)
		}
		if env["TOKEN"] != "from-project" {
			t.Errorf("expected the project's value to win, got %q", env["TOKEN"])
		}
	})

	t.Run("errors name the source and the entry", func(t *testing.T) {
		// No op binary is invoked for a literal, so use a reference that forces
		// the op path and fails (op is not available in tests).
		err := resolveSecretsInto(map[string]string{}, map[string]string{
			"SEGNO_INSTALL_TOKEN": "op://Employee/nope/password",
		}, "preset secret")
		if err == nil {
			t.Skip("op CLI resolved the reference; nothing to assert")
		}
		for _, want := range []string{"preset secret", "SEGNO_INSTALL_TOKEN"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error should mention %q, got: %v", want, err)
			}
		}
	})
}
