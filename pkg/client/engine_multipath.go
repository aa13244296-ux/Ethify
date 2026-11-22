package client

import (
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aa13244296-ux/ethify/pkg/protocol"
)

const (
	maxMultipathPaths         = 4
	multipathHandshakeRetries = 3
)

type multipathPath struct {
	conn           *net.UDPConn
	crypto         *protocol.SessionCrypto
	sessionID      uint64
	relayAddr      netip.AddrPort
	localAddr      netip.Addr
	interfaceIndex int
	health         *multipathPathHealth
}

type multipathPathHealth struct {
	lastAuthenticatedPacket atomic.Int64
}

type multipathState struct {
	mu             sync.RWMutex
	paths          []multipathPath
	localAddresses []string
	groupID        [protocol.DualGroupIDLen]byte
	groupHash      uint32
	nextPath       atomic.Uint32
	nextSeq        atomic.Uint32
}

func validateMultipathAddresses(addresses []string) ([]netip.Addr, error) {
	if len(addresses) < 2 {
		return nil, fmt.Errorf("multipath requires at least two selected network adapters")
	}
	if len(addresses) > maxMultipathPaths {
		return nil, fmt.Errorf("multipath supports at most %d network adapters", maxMultipathPaths)
	}

	seen := make(map[netip.Addr]struct{}, len(addresses))
	result := make([]netip.Addr, 0, len(addresses))
	for _, raw := range addresses {
		address, err := netip.ParseAddr(raw)
		if err != nil || !address.Is4() || address.IsUnspecified() || address.IsLoopback() {
			return nil, fmt.Errorf("invalid multipath IPv4 address %q", raw)
		}
		if _, exists := seen[address]; exists {
			return nil, fmt.Errorf("multipath adapters must use distinct IPv4 addresses")
		}
		seen[address] = struct{}{}
		result = append(result, address)
	}
	return result, nil
}

func openMultipathPath(endpoint string, psk []byte, clientID uint64, expectedIP netip.Addr, groupID [protocol.DualGroupIDLen]byte, localIP netip.Addr, interfaceIndex int) (multipathPath, error) {
	relayUDP, err := net.ResolveUDPAddr("udp4", endpoint)
	if err != nil {
		return multipathPath{}, fmt.Errorf("resolve multipath relay endpoint: %w", err)
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IP(localIP.AsSlice())})
	if err != nil {
		return multipathPath{}, fmt.Errorf("bind multipath socket to %s: %w", localIP, err)
	}
	fail := func(err error) (multipathPath, error) {
		_ = conn.Close()
		return multipathPath{}, err
	}
	if interfaceIndex > 0 {
		if err := bindUDPInterface(conn, interfaceIndex); err != nil {
			return fail(fmt.Errorf("bind multipath socket to interface %d: %w", interfaceIndex, err))
		}
	}
	_ = conn.SetReadBuffer(8 * 1024 * 1024)
	_ = conn.SetWriteBuffer(8 * 1024 * 1024)
	_ = ApplySocketQoS(conn)

	nonce, err := protocol.RandomUint64()
	if err != nil {
		return fail(fmt.Errorf("generate multipath handshake nonce: %w", err))
	}
	request := protocol.HandshakeRequest{
		Nonce:     nonce,
		Timestamp: time.Now().Unix(),
		ClientID:  clientID,
	}
	requestBytes := protocol.EncodeHandshakeRequestDual(psk, request, groupID)
	responseBytes := make([]byte, 256)
	var response *protocol.HandshakeResponse
	var supportsMultipath bool
	for attempt := 0; attempt < multipathHandshakeRetries; attempt++ {
		if _, err := conn.WriteToUDP(requestBytes, relayUDP); err != nil {
			return fail(fmt.Errorf("send multipath handshake on %s: %w", localIP, err))
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, remote, readErr := conn.ReadFromUDP(responseBytes)
		if readErr != nil {
			continue
		}
		if remote.AddrPort().Addr().Unmap() != relayUDP.AddrPort().Addr().Unmap() ||
			remote.AddrPort().Port() != relayUDP.AddrPort().Port() {
			continue
		}
		response, supportsMultipath, err = protocol.DecodeHandshakeResponseExt(psk, responseBytes[:n], nonce)
		if err == nil && response.Status == protocol.StatusOK {
			break
		}
	}
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil || response == nil || response.Status != protocol.StatusOK {
		if err != nil {
			return fail(fmt.Errorf("multipath handshake on %s failed: %w", localIP, err))
		}
		return fail(fmt.Errorf("multipath handshake on %s was rejected", localIP))
	}
	if !supportsMultipath {
		if crypto, cryptoErr := protocol.NewClientCrypto(psk, nonce, response.SessionID, clientID); cryptoErr == nil {
			packet := crypto.EncodeDisconnect(
				make([]byte, protocol.SecureHeaderLen+protocol.TagLen), response.SessionID)
			_, _ = conn.WriteToUDP(packet, relayUDP)
		}
		return fail(fmt.Errorf("relay %s does not support multipath bonding", endpoint))
	}
	if response.ClientIP != expectedIP {
		if crypto, cryptoErr := protocol.NewClientCrypto(psk, nonce, response.SessionID, clientID); cryptoErr == nil {
			packet := crypto.EncodeDisconnect(
				make([]byte, protocol.SecureHeaderLen+protocol.TagLen), response.SessionID)
			_, _ = conn.WriteToUDP(packet, relayUDP)
		}
		return fail(fmt.Errorf("relay assigned mismatched tunnel IP on %s: got %s, expected %s", localIP, response.ClientIP, expectedIP))
	}
	crypto, err := protocol.NewClientCrypto(psk, nonce, response.SessionID, clientID)
	if err != nil {
		return fail(fmt.Errorf("initialize multipath session on %s: %w", localIP, err))
	}

	return multipathPath{
		conn:           conn,
		crypto:         crypto,
		sessionID:      response.SessionID,
		relayAddr:      relayUDP.AddrPort(),
		localAddr:      localIP,
		interfaceIndex: interfaceIndex,
		health:         newMultipathPathHealth(),
	}, nil
}

func newMultipathPathHealth() *multipathPathHealth {
	health := &multipathPathHealth{}
	health.lastAuthenticatedPacket.Store(time.Now().UnixNano())
	return health
}

func (e *Engine) multipathSnapshot() ([]multipathPath, uint32, bool) {
	if e.multipath == nil {
		return nil, 0, false
	}
	e.multipath.mu.RLock()
	defer e.multipath.mu.RUnlock()
	return append([]multipathPath(nil), e.multipath.paths...), e.multipath.groupHash, len(e.multipath.paths) > 0
}

func (e *Engine) isMultipathActive() bool {
	_, _, active := e.multipathSnapshot()
	return active
}

func (e *Engine) multipathAddressesSnapshot() []string {
	if e.multipath == nil {
		return nil
	}
	e.multipath.mu.RLock()
	defer e.multipath.mu.RUnlock()
	return append([]string(nil), e.multipath.localAddresses...)
}

func (e *Engine) nextMultipathPath() (multipathPath, []multipathPath, uint32, bool) {
	paths, groupHash, active := e.multipathSnapshot()
	if !active {
		return multipathPath{}, nil, 0, false
	}
	e.mu.Lock()
	primary := multipathPath{
		conn:      e.udpConn,
		crypto:    e.crypto,
		sessionID: e.sessionID,
		relayAddr: e.relayAddr,
	}
	e.mu.Unlock()
	all := make([]multipathPath, 0, len(paths)+1)
	all = append(all, primary)
	all = append(all, paths...)
	index := e.multipath.nextPath.Add(1) - 1
	return all[index%uint32(len(all))], paths, groupHash, true
}

func (e *Engine) multipathPathForConn(conn *net.UDPConn) (multipathPath, bool) {
	paths, _, active := e.multipathSnapshot()
	if !active {
		return multipathPath{}, false
	}
	for _, path := range paths {
		if path.conn == conn {
			return path, true
		}
	}
	return multipathPath{}, false
}

func (e *Engine) recordMultipathPacket(conn *net.UDPConn) {
	path, ok := e.multipathPathForConn(conn)
	if ok && path.health != nil {
		path.health.lastAuthenticatedPacket.Store(time.Now().UnixNano())
	}
}

func (e *Engine) multipathHealthError(now time.Time) string {
	paths, _, active := e.multipathSnapshot()
	if !active {
		return ""
	}
	for _, path := range paths {
		if path.health == nil {
			return fmt.Sprintf("adapter path %s has no health state", path.localAddr)
		}
		last := path.health.lastAuthenticatedPacket.Load()
		if last == 0 || now.Sub(time.Unix(0, last)) > 10*time.Second {
			return fmt.Sprintf("adapter path %s stopped responding", path.localAddr)
		}
	}
	return ""
}

func (e *Engine) closeMultipathPaths() []multipathPath {
	if e.multipath == nil {
		return nil
	}
	e.multipath.mu.Lock()
	paths := e.multipath.paths
	e.multipath.paths = nil
	e.multipath.localAddresses = nil
	e.multipath.groupID = [protocol.DualGroupIDLen]byte{}
	e.multipath.groupHash = 0
	e.multipath.mu.Unlock()
	for _, path := range paths {
		if path.conn != nil {
			closeMultipathPath(path)
		}
	}
	return paths
}

func closeMultipathPath(path multipathPath) {
	if path.conn == nil {
		return
	}
	if path.crypto != nil {
		packet := path.crypto.EncodeDisconnect(
			make([]byte, protocol.SecureHeaderLen+protocol.TagLen), path.sessionID)
		_, _ = path.conn.WriteToUDP(packet, net.UDPAddrFromAddrPort(path.relayAddr))
	}
	_ = path.conn.Close()
}

func (e *Engine) sendMultipathKeepalives() {
	paths, _, active := e.multipathSnapshot()
	if !active {
		return
	}
	for _, path := range paths {
		if path.conn == nil || path.crypto == nil {
			continue
		}
		packet := path.crypto.EncodePing(
			make([]byte, protocol.SecureHeaderLen+8+protocol.TagLen),
			path.sessionID, uint64(time.Now().UnixNano()))
		_, _ = path.conn.WriteToUDP(packet, net.UDPAddrFromAddrPort(path.relayAddr))
	}
}
