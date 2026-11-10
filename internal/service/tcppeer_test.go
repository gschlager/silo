package service

import (
	"net"
	"os"
	"testing"
)

func TestTCPPeerUID(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "[::1]:0"} {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Logf("%s: %v", addr, err)
			continue
		}
		accepted := make(chan net.Conn, 1)
		go func() {
			c, _ := l.Accept()
			accepted <- c
		}()
		client, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		server := <-accepted
		uid, err := tcpPeerUID(server)
		if err != nil || uid != os.Getuid() {
			t.Errorf("%s: tcpPeerUID = %d, %v; want %d", addr, uid, err, os.Getuid())
		}
		client.Close()
		server.Close()
		l.Close()
	}
}

func TestParseProcAddr(t *testing.T) {
	a, err := parseProcAddr("0100007F:1F90")
	if err != nil || a.String() != "127.0.0.1:8080" {
		t.Errorf("IPv4: %v %v", a, err)
	}
	a, err = parseProcAddr("00000000000000000000000001000000:0050")
	if err != nil || a.String() != "[::1]:80" {
		t.Errorf("IPv6: %v %v", a, err)
	}
}
