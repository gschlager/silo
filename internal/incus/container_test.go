package incus

import "testing"

func TestSplitImage(t *testing.T) {
	cases := []struct {
		image           string
		distro, release string
		ok              bool
	}{
		{"fedora/44", "fedora", "44", true},
		{"images:ubuntu/24.04", "ubuntu", "24.04", true},
		{"local:fedora/43", "fedora", "43", true},
		{"fedora", "", "", false},
	}
	for _, c := range cases {
		distro, release, ok := splitImage(c.image)
		if distro != c.distro || release != c.release || ok != c.ok {
			t.Errorf("splitImage(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.image, distro, release, ok, c.distro, c.release, c.ok)
		}
	}
}
