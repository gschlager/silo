package incus

import (
	"fmt"

	incus "github.com/lxc/incus/v6/client"
)

// Connect returns a connection to the local Incus daemon.
func Connect() (incus.InstanceServer, error) {
	server, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		return nil, fmt.Errorf("connecting to Incus: %w", err)
	}
	return server, nil
}
