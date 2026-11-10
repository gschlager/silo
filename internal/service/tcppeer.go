package service

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// tcpPeerUID returns the uid that owns the client side of a loopback TCP
// connection, from /proc/net/tcp or /proc/net/tcp6. The client's socket is
// listed there with its local address (our remote address) and its owner.
// This is how the web UI only answers processes running as this user: the
// proxy ports are on loopback, but other users on the machine could reach
// them too.
func tcpPeerUID(c net.Conn) (int, error) {
	local, ok1 := c.LocalAddr().(*net.TCPAddr)
	remote, ok2 := c.RemoteAddr().(*net.TCPAddr)
	if !ok1 || !ok2 {
		return -1, fmt.Errorf("not a TCP connection")
	}
	if !remote.IP.IsLoopback() {
		return -1, fmt.Errorf("not a loopback connection")
	}
	files := []string{"/proc/net/tcp6", "/proc/net/tcp"}
	if remote.IP.To4() != nil {
		files = []string{"/proc/net/tcp", "/proc/net/tcp6"}
	}
	for _, f := range files {
		if uid, err := findSocketOwner(f, remote, local); err == nil {
			return uid, nil
		}
	}
	return -1, fmt.Errorf("no socket for %s → %s", remote, local)
}

// findSocketOwner looks for the socket with the given local and remote
// address in a /proc/net/tcp* file and returns its uid.
func findSocketOwner(file string, sockLocal, sockRemote *net.TCPAddr) (int, error) {
	f, err := os.Open(file)
	if err != nil {
		return -1, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 8 {
			continue
		}
		l, err1 := parseProcAddr(fields[1])
		r, err2 := parseProcAddr(fields[2])
		if err1 != nil || err2 != nil {
			continue
		}
		if sameAddr(l, sockLocal) && sameAddr(r, sockRemote) {
			return strconv.Atoi(fields[7])
		}
	}
	return -1, fmt.Errorf("not found")
}

func sameAddr(a, b *net.TCPAddr) bool {
	return a.Port == b.Port && a.IP.Equal(b.IP)
}

// parseProcAddr parses "0100007F:1F90" (IPv4) or a 32-hex-digit IPv6 address
// with a port. The address is stored as 32-bit words in host byte order.
func parseProcAddr(s string) (*net.TCPAddr, error) {
	ipHex, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return nil, fmt.Errorf("bad address %q", s)
	}
	raw, err := hex.DecodeString(ipHex)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return nil, fmt.Errorf("bad address %q", s)
	}
	ip := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.BigEndian.PutUint32(ip[i:], binary.NativeEndian.Uint32(raw[i:]))
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return nil, err
	}
	return &net.TCPAddr{IP: ip, Port: int(port)}, nil
}
