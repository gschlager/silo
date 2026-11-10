package service

import (
	"os"
	"testing"
)

// The RPM ships packaging/silo.{socket,service}; they must match what
// `silo setup` writes for other installs.
func TestPackagedUnitsMatch(t *testing.T) {
	for file, want := range map[string]string{
		"../../packaging/silo.socket":  SocketUnit(),
		"../../packaging/silo.service": ServiceUnit("/usr/bin/silo"),
	} {
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s differs from the unit silo setup writes:\n%s\nwant:\n%s", file, got, want)
		}
	}
}
