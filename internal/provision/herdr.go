package provision

import (
	"os/exec"
	"strings"

	"github.com/gschlager/silo/internal/color"
)

// FindHerdrHC returns the path of herdr's hc when it's installed on the
// host. "hc" is a short name, so it only counts if `hc --version` answers
// like herdr-core does ("hc 0.1.17").
func FindHerdrHC() (string, bool) {
	hc, err := exec.LookPath("hc")
	if err != nil {
		return "", false
	}
	out, err := exec.Command(hc, "--version").Output()
	if err != nil || !IsHerdrVersion(string(out)) {
		color.Debug("%s is not herdr's hc: %v %q", hc, err, out)
		return "", false
	}
	return hc, true
}

// IsHerdrVersion reports whether out is herdr-core's version line.
func IsHerdrVersion(out string) bool {
	v, ok := strings.CutPrefix(strings.TrimSpace(out), "hc ")
	return ok && v != "" && v[0] >= '0' && v[0] <= '9'
}
