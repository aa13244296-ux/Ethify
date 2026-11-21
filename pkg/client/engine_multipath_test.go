package client

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/ThanhNguyxnOrg/lagvex/pkg/protocol"
)

func TestMultipathHealthDetectsStaleAdapter(t *testing.T) {
	stale := &multipathPathHealth{}
	stale.lastAuthenticatedPacket.Store(time.Now().Add(-11 * time.Second).UnixNano())
	engine := &Engine{
		multipath: &multipathState{
			paths: []multipathPath{{
				localAddr: netip.MustParseAddr("192.0.2.10"),
				health:    stale,
			}},
		},
	}
	if got := engine.multipathHealthError(time.Now()); got == "" {
		t.Fatal("stale adapter path was reported healthy")
	}

	fresh := &multipathPathHealth{}
	fresh.lastAuthenticatedPacket.Store(time.Now().UnixNano())
	engine.multipath.mu.Lock()
	engine.multipath.paths[0].health = fresh
	engine.multipath.mu.Unlock()
	if got := engine.multipathHealthError(time.Now()); got != "" {
		t.Fatalf("fresh adapter path reported unhealthy: %s", got)
	}
}

func TestValidateMultipathAddresses(t *testing.T) {
	tests := []struct {
		name      string
		addresses []string
		wantErr   bool
	}{
		{name: "two distinct adapters", addresses: []string{"192.0.2.10", "198.51.100.20"}},
		{name: "too few adapters", addresses: []string{"192.0.2.10"}, wantErr: true},
		{name: "duplicate adapter address", addresses: []string{"192.0.2.10", "192.0.2.10"}, wantErr: true},
		{name: "invalid address", addresses: []string{"192.0.2.10", "not-an-ip"}, wantErr: true},
		{name: "loopback address", addresses: []string{"192.0.2.10", "127.0.0.1"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validateMultipathAddresses(test.addresses)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateMultipathAddresses(%v) error = %v, wantErr %v",
					test.addresses, err, test.wantErr)
			}
		})
	}
}

func TestOpenMultipathPathsShareRelayAssignedIP(t *testing.T) {
	psk := []byte("client-multipath-test-psk")
	serverConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()

	ctxDone := make(chan struct{})
	go func() {
		defer close(ctxDone)
		buf := make([]byte, 2048)
		var sessionID uint64 = 100
		for {
			n, remote, err := serverConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			request, groupID, err := protocol.DecodeHandshakeRequestExt(psk, buf[:n])
			if err != nil || groupID == nil {
				continue
			}
			sessionID++
			response := protocol.HandshakeResponse{
				Status:    protocol.StatusOK,
				SessionID: sessionID,
				ClientIP:  netip.MustParseAddr("10.77.0.2"),
				GatewayIP: netip.MustParseAddr("10.77.0.1"),
				MTU:       1400,
				NonceEcho: request.Nonce,
			}
			_, _ = serverConn.WriteToUDP(
				protocol.EncodeHandshakeResponseDual(psk, response), remote)
		}
	}()
	defer func() {
		_ = serverConn.Close()
		<-ctxDone
	}()

	groupID := [protocol.DualGroupIDLen]byte{1, 2, 3, 4, 5}
	clientID := uint64(77)
	expectedIP := netip.MustParseAddr("10.77.0.2")
	endpoint := serverConn.LocalAddr().String()

	first, err := openMultipathPath(endpoint, psk, clientID, expectedIP, groupID,
		netip.MustParseAddr("127.0.0.1"), 0)
	if err != nil {
		t.Fatalf("open first path: %v", err)
	}
	defer closeMultipathPath(first)

	second, err := openMultipathPath(endpoint, psk, clientID, expectedIP, groupID,
		netip.MustParseAddr("127.0.0.1"), 0)
	if err != nil {
		t.Fatalf("open second path: %v", err)
	}
	defer closeMultipathPath(second)
	if first.sessionID == second.sessionID {
		t.Fatal("relay reused a session ID across independent paths")
	}
}
