package agents

import "testing"

func TestDeepMerge(t *testing.T) {
	t.Run("simple override", func(t *testing.T) {
		dst := map[string]any{"a": 1, "b": 2}
		src := map[string]any{"b": 3, "c": 4}
		deepMerge(dst, src)

		if dst["a"] != 1 {
			t.Errorf("a = %v, want 1", dst["a"])
		}
		if dst["b"] != 3 {
			t.Errorf("b = %v, want 3", dst["b"])
		}
		if dst["c"] != 4 {
			t.Errorf("c = %v, want 4", dst["c"])
		}
	})

	t.Run("nested merge", func(t *testing.T) {
		dst := map[string]any{
			"top": map[string]any{
				"keep": "yes",
				"over": "old",
			},
		}
		src := map[string]any{
			"top": map[string]any{
				"over": "new",
				"add":  "extra",
			},
		}
		deepMerge(dst, src)

		top := dst["top"].(map[string]any)
		if top["keep"] != "yes" {
			t.Errorf("top.keep = %v, want yes", top["keep"])
		}
		if top["over"] != "new" {
			t.Errorf("top.over = %v, want new", top["over"])
		}
		if top["add"] != "extra" {
			t.Errorf("top.add = %v, want extra", top["add"])
		}
	})

	t.Run("overwrite non-map with map", func(t *testing.T) {
		dst := map[string]any{"a": "string"}
		src := map[string]any{"a": map[string]any{"nested": true}}
		deepMerge(dst, src)

		if _, ok := dst["a"].(map[string]any); !ok {
			t.Errorf("a should be a map, got %T", dst["a"])
		}
	})
}

func TestDir(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/home/dev/.claude/settings.json", "/home/dev/.claude"},
		{"/a/b/c", "/a/b"},
		{"nodir", "."},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := dir(tt.path)
			if got != tt.want {
				t.Errorf("dir(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
