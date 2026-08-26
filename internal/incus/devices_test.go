package incus

import "testing"

func TestProxyListenAddressIsLoopbackOnly(t *testing.T) {
	if got, want := proxyListenAddress(15432), "tcp:127.0.0.1:15432"; got != want {
		t.Fatalf("proxyListenAddress = %q, want %q", got, want)
	}
}
