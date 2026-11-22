package relay

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/aa13244296-ux/ethify/pkg/protocol"
)

func TestGroupedHandshakesShareOneClientIP(t *testing.T) {
	psk := []byte("test-psk")
	server, err := NewServer(Config{
		ListenAddr: ":51820",
		Subnet:     netip.MustParsePrefix("10.88.0.0/24"),
		PSK:        psk,
		MTU:        1400,
		MaxClients: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()
	server.conn = serverConn

	clientConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	groupID := [protocol.DualGroupIDLen]byte{1, 2, 3, 4, 5}
	clientID := uint64(41)
	remote := clientConn.LocalAddr().(*net.UDPAddr).AddrPort()
	connectPath := func(nonce uint64) protocol.HandshakeResponse {
		t.Helper()
		req := protocol.HandshakeRequest{
			Nonce:     nonce,
			Timestamp: time.Now().Unix(),
			ClientID:  clientID,
		}
		server.handleHandshake(protocol.EncodeHandshakeRequestDual(psk, req, groupID), remote)
		if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 256)
		n, _, err := clientConn.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("read handshake response: %v", err)
		}
		resp, supportsMultipath, err := protocol.DecodeHandshakeResponseExt(psk, buf[:n], nonce)
		if err != nil {
			t.Fatalf("decode handshake response: %v", err)
		}
		if resp.Status != protocol.StatusOK {
			t.Fatalf("handshake status = %d, want OK", resp.Status)
		}
		if !supportsMultipath {
			t.Fatal("relay did not advertise multipath support")
		}
		return *resp
	}

	first := connectPath(1)
	retry := connectPath(1)
	if retry.SessionID != first.SessionID {
		t.Fatalf("repeated handshake changed session: got %016x, want %016x", retry.SessionID, first.SessionID)
	}
	if got := server.activeSessions.Load(); got != 1 {
		t.Fatalf("repeated handshake created another path: active count = %d, want 1", got)
	}
	second := connectPath(2)
	if first.ClientIP != second.ClientIP {
		t.Fatalf("group path IPs differ: %s and %s", first.ClientIP, second.ClientIP)
	}
	if got := server.activeSessions.Load(); got != 2 {
		t.Fatalf("active path count = %d, want 2", got)
	}
	if got := len(server.pathsByIP[first.ClientIP]); got != 2 {
		t.Fatalf("paths for shared IP = %d, want 2", got)
	}

	connectPath(3)
	connectPath(4)
	req := protocol.HandshakeRequest{
		Nonce:     5,
		Timestamp: time.Now().Unix(),
		ClientID:  clientID,
	}
	server.handleHandshake(protocol.EncodeHandshakeRequestDual(psk, req, groupID), remote)
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, _, err := clientConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read full-group response: %v", err)
	}
	rejected, _, err := protocol.DecodeHandshakeResponseExt(psk, buf[:n], 0)
	if err != nil {
		t.Fatalf("decode full-group response: %v", err)
	}
	if rejected.Status != protocol.StatusServerBusy {
		t.Fatalf("fifth group path status = %d, want busy", rejected.Status)
	}
	if got := server.activeSessions.Load(); got != maxRelayGroupPaths {
		t.Fatalf("active path count after rejected handshake = %d, want %d", got, maxRelayGroupPaths)
	}

	otherClient := req
	otherClient.Nonce = 6
	otherClient.ClientID++
	otherGroup := [protocol.DualGroupIDLen]byte{9, 8, 7}
	server.handleHandshake(protocol.EncodeHandshakeRequestDual(psk, otherClient, otherGroup), remote)
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	n, _, err = clientConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read full-server response: %v", err)
	}
	rejected, _, err = protocol.DecodeHandshakeResponseExt(psk, buf[:n], 0)
	if err != nil {
		t.Fatalf("decode full-server response: %v", err)
	}
	if rejected.Status != protocol.StatusServerBusy {
		t.Fatalf("second client status = %d, want busy", rejected.Status)
	}

	req.Nonce = 7
	server.handleHandshake(protocol.EncodeHandshakeRequest(psk, req), remote)
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	n, _, err = clientConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read plain reconnect response: %v", err)
	}
	replacement, hasMultipath, err := protocol.DecodeHandshakeResponseExt(psk, buf[:n], req.Nonce)
	if err != nil {
		t.Fatalf("decode plain reconnect response: %v", err)
	}
	if replacement.Status != protocol.StatusOK || hasMultipath {
		t.Fatalf("plain reconnect response = status %d, multipath %v; want OK, false",
			replacement.Status, hasMultipath)
	}
	if got := server.activeSessions.Load(); got != 1 {
		t.Fatalf("active path count after ungrouped reconnect = %d, want 1", got)
	}
	if got := len(server.pathsByIP[replacement.ClientIP]); got != 1 {
		t.Fatalf("paths after ungrouped reconnect = %d, want 1", got)
	}

	clientCrypto, err := protocol.NewClientCrypto(psk, req.Nonce, replacement.SessionID, clientID)
	if err != nil {
		t.Fatalf("create client crypto: %v", err)
	}
	packet := clientCrypto.EncodeDisconnect(
		make([]byte, protocol.SecureHeaderLen+protocol.TagLen), replacement.SessionID)
	server.handleSessionPacket(packet, remote)
	if got := server.activeSessions.Load(); got != 0 {
		t.Fatalf("active path count after disconnect = %d, want 0", got)
	}
	if got := server.selectDownlinkSession(replacement.ClientIP); got != nil {
		t.Fatalf("downlink path after final disconnect = %p, want nil", got)
	}
}

func TestMultipathSessionsShareIPUntilLastPathIsRemoved(t *testing.T) {
	server, err := NewServer(Config{
		ListenAddr: ":51820",
		Subnet:     netip.MustParsePrefix("10.88.0.0/24"),
		PSK:        []byte("test-psk"),
		MTU:        1400,
	})
	if err != nil {
		t.Fatal(err)
	}

	clientID := uint64(41)
	innerIP, _, err := server.pool.Allocate(clientID, 101)
	if err != nil {
		t.Fatal(err)
	}
	first := NewSession(101, clientID, innerIP, netip.MustParseAddrPort("192.0.2.1:1001"), nil)
	second := NewSession(102, clientID, innerIP, netip.MustParseAddrPort("192.0.2.2:1002"), nil)
	first.GroupHash = 7
	second.GroupHash = 7

	server.mu.Lock()
	server.sessionsByID[first.ID] = first
	server.sessionsByID[second.ID] = second
	server.sessionsByIP[innerIP] = first
	server.pathsByIP[innerIP] = []*Session{first, second}
	server.activeSessions.Store(2)
	if !server.removeSessionLocked(first, false) {
		t.Fatal("expected first path to be removed")
	}
	server.mu.Unlock()

	if got := server.selectDownlinkSession(innerIP); got != second {
		t.Fatalf("remaining downlink path = %p, want second path %p", got, second)
	}
	if got := server.activeSessions.Load(); got != 1 {
		t.Fatalf("active path count = %d, want 1", got)
	}

	server.mu.Lock()
	server.removeSessionLocked(second, false)
	server.mu.Unlock()
	if got := server.selectDownlinkSession(innerIP); got != nil {
		t.Fatalf("downlink path after last disconnect = %p, want nil", got)
	}

	reallocated, _, err := server.pool.Allocate(clientID+1, 103)
	if err != nil {
		t.Fatal(err)
	}
	if reallocated != innerIP {
		t.Fatalf("released IP was not returned to pool: got %s, want %s", reallocated, innerIP)
	}
}

func TestMultipathDownlinkSelectionBalancesPaths(t *testing.T) {
	server, err := NewServer(Config{
		ListenAddr: ":51820",
		Subnet:     netip.MustParsePrefix("10.88.0.0/24"),
		PSK:        []byte("test-psk"),
		MTU:        1400,
	})
	if err != nil {
		t.Fatal(err)
	}

	innerIP := netip.MustParseAddr("10.88.0.2")
	first := NewSession(101, 41, innerIP, netip.MustParseAddrPort("192.0.2.1:1001"), nil)
	second := NewSession(102, 41, innerIP, netip.MustParseAddrPort("192.0.2.2:1002"), nil)
	server.pathsByIP[innerIP] = []*Session{first, second}

	counts := map[*Session]int{}
	for i := 0; i < 20; i++ {
		counts[server.selectDownlinkSession(innerIP)]++
	}
	if counts[first] != 10 || counts[second] != 10 {
		t.Fatalf("downlink path distribution = first:%d second:%d, want 10 each", counts[first], counts[second])
	}
}
