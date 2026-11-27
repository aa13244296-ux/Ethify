//go:build linux

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"github.com/xjasonlyu/tun2socks/v2/core/device/iobased"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	linuxRouterInterface = "ethify0"
	linuxRouterAddress   = "10.77.0.1/24"
	linuxRouterMTU       = 1500
	linuxRouterMark      = 0x45544859
	linuxRouterTable     = 4242
	linuxRouterMarkRule  = 10000
	linuxRouterMainRule  = 10001
	linuxRouterTCPIdle   = 30 * time.Second
)

type linuxLocalRouter struct {
	mu       sync.Mutex
	adapter  *WintunAdapter
	stack    *stack.Stack
	flows    *linuxRouterFlows
	networks []DownloadNetwork
	lastErr  string
	rateCaps map[int]uint64
}

func newLocalSystemRouter() localSystemRouter { return &linuxLocalRouter{} }

func (r *linuxLocalRouter) Enable() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapter != nil {
		return nil
	}
	if !linuxCapabilityEffective(unixCapNetAdmin) {
		return errors.New("the Linux system router requires root or CAP_NET_ADMIN")
	}
	if !linuxCapabilityEffective(unixCapNetRaw) {
		return errors.New("the Linux system router requires root or CAP_NET_RAW for bound egress sockets")
	}
	if err := cleanupStaleLinuxRouterState(); err != nil {
		return fmt.Errorf("linux router preflight: %w", err)
	}
	networks, err := ListDownloadNetworks()
	if err != nil {
		return fmt.Errorf("find active network adapters: %w", err)
	}
	if len(networks) < 2 {
		return errors.New("connect at least two active IPv4 adapters before enabling the Linux system router")
	}

	adapter, err := OpenOrCreateWintunAdapter(linuxRouterInterface, "", "")
	if err != nil {
		return fmt.Errorf("open Linux TUN adapter: %w", err)
	}
	cleanupAdapter := true
	defer func() {
		if cleanupAdapter {
			_ = adapter.Close()
		}
	}()
	if err := runLinuxIP("addr", "replace", linuxRouterAddress, "dev", adapter.name); err != nil {
		return fmt.Errorf("configure TUN address: %w", err)
	}
	if err := runLinuxIP("link", "set", "dev", adapter.name, "mtu", strconv.Itoa(linuxRouterMTU), "up"); err != nil {
		return fmt.Errorf("bring TUN adapter up: %w", err)
	}
	device, err := iobased.New(linuxTunReadWriter{adapter: adapter}, linuxRouterMTU, 0)
	if err != nil {
		return fmt.Errorf("create packet device: %w", err)
	}
	flows := newLinuxRouterFlows(networks)
	tunnelStack, err := core.CreateStack(&core.Config{
		LinkEndpoint:     device,
		TransportHandler: flows,
	})
	if err != nil {
		flows.Close()
		return fmt.Errorf("create Linux TCP/IP stack: %w", err)
	}
	cleanupStack := true
	defer func() {
		if cleanupStack {
			flows.Close()
			tunnelStack.Close()
		}
	}()

	activeLinuxRouterMark.Store(linuxRouterMark)
	if err := installLinuxRouterPolicy(adapter.name); err != nil {
		activeLinuxRouterMark.Store(0)
		return fmt.Errorf("install Linux policy routes: %w", err)
	}
	r.adapter = adapter
	r.stack = tunnelStack
	r.flows = flows
	r.networks = networks
	r.lastErr = ""
	cleanupAdapter = false
	cleanupStack = false
	log.Printf("[Local Router] Enabled Linux TUN %s across %d adapters", adapter.name, len(networks))
	return nil
}

func (r *linuxLocalRouter) Disable() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapter == nil {
		return nil
	}
	if err := removeLinuxRouterPolicy(); err != nil {
		r.lastErr = err.Error()
		return err
	}
	activeLinuxRouterMark.Store(0)
	if r.flows != nil {
		r.flows.Close()
	}
	if r.stack != nil {
		r.stack.Close()
	}
	_ = runLinuxIP("link", "set", "dev", r.adapter.name, "down")
	err := r.adapter.Close()
	r.adapter = nil
	r.stack = nil
	r.flows = nil
	r.networks = nil
	if err != nil {
		r.lastErr = fmt.Sprintf("close Linux TUN adapter: %v", err)
		return errors.New(r.lastErr)
	}
	r.lastErr = ""
	return nil
}

func (r *linuxLocalRouter) SetAdapterRateLimits(limits map[int]uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rateCaps = make(map[int]uint64, len(limits))
	for index, limit := range limits {
		r.rateCaps[index] = limit
	}
	return nil
}

func (r *linuxLocalRouter) Status() localRouterStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := localRouterStatus{
		Active:      r.adapter != nil,
		Available:   true,
		Elevated:    linuxCapabilityEffective(unixCapNetAdmin) && linuxCapabilityEffective(unixCapNetRaw),
		Platform:    "linux",
		Networks:    append([]DownloadNetwork(nil), r.networks...),
		LastError:   r.lastErr,
		EgressLimit: localRouterEgressLimit,
	}
	if !status.Elevated && status.LastError == "" {
		status.LastError = "Linux router requires root or CAP_NET_ADMIN and CAP_NET_RAW."
	}
	if r.flows != nil {
		status.TCPFlows = r.flows.tcpActive.Load()
		status.UDPFlows = r.flows.udpActive.Load()
		status.TCPDialAttempts = r.flows.tcpAttempts.Load()
		status.UDPDialAttempts = r.flows.udpAttempts.Load()
		status.TCPConnected = r.flows.tcpConnected.Load()
		status.UDPConnected = r.flows.udpConnected.Load()
		status.ActiveEgress = status.TCPFlows + status.UDPFlows
	}
	return status
}

func (r *linuxLocalRouter) Close() error { return r.Disable() }

type linuxTunReadWriter struct{ adapter *WintunAdapter }

func (rw linuxTunReadWriter) Read(packet []byte) (int, error) {
	return rw.adapter.ReadPacket(packet)
}

func (rw linuxTunReadWriter) Write(packet []byte) (int, error) {
	if err := rw.adapter.WritePacket(packet); err != nil {
		return 0, err
	}
	return len(packet), nil
}

type linuxRouterFlows struct {
	ctx          context.Context
	cancel       context.CancelFunc
	networks     []DownloadNetwork
	next         atomic.Uint64
	tcpActive    atomic.Uint64
	udpActive    atomic.Uint64
	tcpAttempts  atomic.Uint64
	udpAttempts  atomic.Uint64
	tcpConnected atomic.Uint64
	udpConnected atomic.Uint64
	mu           sync.Mutex
	closed       bool
	active       map[io.Closer]struct{}
	wg           sync.WaitGroup
}

func newLinuxRouterFlows(networks []DownloadNetwork) *linuxRouterFlows {
	ctx, cancel := context.WithCancel(context.Background())
	return &linuxRouterFlows{
		ctx:      ctx,
		cancel:   cancel,
		networks: append([]DownloadNetwork(nil), networks...),
		active:   make(map[io.Closer]struct{}),
	}
}

func (h *linuxRouterFlows) register(closers ...io.Closer) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		for _, closer := range closers {
			_ = closer.Close()
		}
		return false
	}
	for _, closer := range closers {
		h.active[closer] = struct{}{}
	}
	h.wg.Add(1)
	return true
}

func (h *linuxRouterFlows) unregister(closers ...io.Closer) {
	h.mu.Lock()
	for _, closer := range closers {
		delete(h.active, closer)
	}
	h.mu.Unlock()
	h.wg.Done()
}

func (h *linuxRouterFlows) Close() {
	h.mu.Lock()
	h.closed = true
	h.cancel()
	for closer := range h.active {
		_ = closer.Close()
	}
	h.mu.Unlock()
	h.wg.Wait()
}

func (h *linuxRouterFlows) HandleTCP(conn adapter.TCPConn) {
	if h.tcpActive.Load() >= localRouterEgressLimit {
		_ = conn.Close()
		return
	}
	h.tcpActive.Add(1)
	h.tcpAttempts.Add(1)
	id := conn.ID()
	destination := net.JoinHostPort(netip.AddrFrom4(id.LocalAddress.As4()).String(), strconv.Itoa(int(id.LocalPort)))
	network := h.nextNetwork()
	remote, err := dialLinuxRouterTCP(h.ctx, network, destination)
	if err != nil {
		h.tcpActive.Add(^uint64(0))
		_ = conn.Close()
		log.Printf("[Local Router] TCP dial %s via %s failed: %v", destination, network.Name, err)
		return
	}
	h.tcpConnected.Add(1)
	if !h.register(conn, remote) {
		h.tcpActive.Add(^uint64(0))
		return
	}
	go func() {
		defer h.tcpActive.Add(^uint64(0))
		defer h.unregister(conn, remote)
		linuxRouterTCPPipe(h.ctx, conn, remote)
	}()
}

func (h *linuxRouterFlows) HandleUDP(conn adapter.UDPConn) {
	if h.udpActive.Load() >= 32 {
		_ = conn.Close()
		return
	}
	h.udpActive.Add(1)
	h.udpAttempts.Add(1)
	id := conn.ID()
	destination := net.JoinHostPort(netip.AddrFrom4(id.LocalAddress.As4()).String(), strconv.Itoa(int(id.LocalPort)))
	network := h.nextNetwork()
	remote, err := dialLinuxRouterUDP(h.ctx, network)
	if err != nil {
		h.udpActive.Add(^uint64(0))
		_ = conn.Close()
		log.Printf("[Local Router] UDP dial %s via %s failed: %v", destination, network.Name, err)
		return
	}
	h.udpConnected.Add(1)
	if !h.register(conn, remote) {
		h.udpActive.Add(^uint64(0))
		return
	}
	go func() {
		defer h.udpActive.Add(^uint64(0))
		defer h.unregister(conn, remote)
		linuxRouterUDPPipe(h.ctx, conn, remote, destination)
	}()
}

func (h *linuxRouterFlows) nextNetwork() DownloadNetwork {
	index := (h.next.Add(1) - 1) % uint64(len(h.networks))
	return h.networks[index]
}

func dialLinuxRouterTCP(ctx context.Context, network DownloadNetwork, destination string) (net.Conn, error) {
	address, err := netip.ParseAddr(network.Address)
	if err != nil || !address.Is4() {
		return nil, fmt.Errorf("adapter %s has invalid IPv4 address", network.Name)
	}
	dialer := &net.Dialer{
		Timeout:   3 * time.Second,
		KeepAlive: 30 * time.Second,
		LocalAddr: &net.TCPAddr{IP: net.IP(address.AsSlice())},
		Control: func(_, _ string, raw syscall.RawConn) error {
			return bindTCPInterface(raw, network.InterfaceIndex)
		},
	}
	return dialer.DialContext(ctx, "tcp4", destination)
}

func dialLinuxRouterUDP(ctx context.Context, network DownloadNetwork) (net.PacketConn, error) {
	address, err := netip.ParseAddr(network.Address)
	if err != nil || !address.Is4() {
		return nil, fmt.Errorf("adapter %s has invalid IPv4 address", network.Name)
	}
	listen := net.ListenConfig{
		Control: func(_, _ string, raw syscall.RawConn) error {
			return bindTCPInterface(raw, network.InterfaceIndex)
		},
	}
	return listen.ListenPacket(ctx, "udp4", net.JoinHostPort(address.String(), "0"))
}

func linuxRouterTCPPipe(ctx context.Context, origin, remote net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(remote, origin)
		if halfCloser, ok := remote.(interface{ CloseWrite() error }); ok {
			_ = halfCloser.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(origin, remote)
		if halfCloser, ok := origin.(interface{ CloseWrite() error }); ok {
			_ = halfCloser.CloseWrite()
		}
	}()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		_ = origin.Close()
		_ = remote.Close()
		<-done
	}
}

func linuxRouterUDPPipe(ctx context.Context, origin, remote net.PacketConn, destination string) {
	address, err := net.ResolveUDPAddr("udp4", destination)
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		packet := make([]byte, 65535)
		for {
			_ = origin.SetReadDeadline(time.Now().Add(linuxRouterTCPIdle))
			n, _, readErr := origin.ReadFrom(packet)
			if readErr != nil {
				return
			}
			if _, writeErr := remote.WriteTo(packet[:n], address); writeErr != nil {
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		packet := make([]byte, 65535)
		for {
			_ = remote.SetReadDeadline(time.Now().Add(linuxRouterTCPIdle))
			n, from, readErr := remote.ReadFrom(packet)
			if readErr != nil {
				return
			}
			if from != nil && from.String() == address.String() {
				if _, writeErr := origin.WriteTo(packet[:n], nil); writeErr != nil {
					return
				}
			}
		}
	}()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		_ = origin.Close()
		_ = remote.Close()
		<-done
	}
}

func installLinuxRouterPolicy(interfaceName string) error {
	if err := runLinuxIP("-4", "route", "replace", "default", "dev", interfaceName, "table", strconv.Itoa(linuxRouterTable)); err != nil {
		return err
	}
	if err := runLinuxIP("-4", "rule", "add", "priority", strconv.Itoa(linuxRouterMarkRule), "fwmark", fmt.Sprintf("0x%x/0xffffffff", linuxRouterMark), "lookup", "main"); err != nil {
		_ = runLinuxIP("-4", "route", "flush", "table", strconv.Itoa(linuxRouterTable))
		return err
	}
	if err := runLinuxIP("-4", "rule", "add", "priority", strconv.Itoa(linuxRouterMainRule), "lookup", strconv.Itoa(linuxRouterTable)); err != nil {
		_ = runLinuxIP("-4", "rule", "del", "priority", strconv.Itoa(linuxRouterMarkRule))
		_ = runLinuxIP("-4", "route", "flush", "table", strconv.Itoa(linuxRouterTable))
		return err
	}
	return nil
}

func removeLinuxRouterPolicy() error {
	var errs []error
	if err := removeLinuxRouterRule(linuxRouterMainRule, "lookup "+strconv.Itoa(linuxRouterTable)); err != nil {
		errs = append(errs, fmt.Errorf("remove Ethify policy rule: %w", err))
	}
	if err := removeLinuxRouterRule(linuxRouterMarkRule, fmt.Sprintf("fwmark 0x%x", linuxRouterMark)); err != nil {
		errs = append(errs, fmt.Errorf("remove Ethify mark rule: %w", err))
	}
	if err := runLinuxIP("-4", "route", "flush", "table", strconv.Itoa(linuxRouterTable)); err != nil {
		errs = append(errs, fmt.Errorf("flush Ethify routing table: %w", err))
	}
	return errors.Join(errs...)
}

func removeLinuxRouterRule(priority int, expected string) error {
	output, err := runLinuxIPOutput("-4", "rule", "show")
	if err != nil {
		return fmt.Errorf("inspect policy rules: %w", err)
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		left, right, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(left) != strconv.Itoa(priority) {
			continue
		}
		if !strings.Contains(right, expected) {
			return fmt.Errorf("policy priority %d is now owned by another rule", priority)
		}
		return runLinuxIP("-4", "rule", "del", "priority", strconv.Itoa(priority))
	}
	return nil
}

func cleanupStaleLinuxRouterState() error {
	output, err := runLinuxIPOutput("-4", "rule", "show")
	if err != nil {
		return fmt.Errorf("inspect existing policy rules: %w", err)
	}
	seen := map[int]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		priority, remainder, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value, parseErr := strconv.Atoi(strings.TrimSpace(priority))
		if parseErr != nil || (value != linuxRouterMarkRule && value != linuxRouterMainRule) {
			continue
		}
		seen[value] = strings.TrimSpace(remainder)
	}
	if rule, ok := seen[linuxRouterMarkRule]; ok {
		if !strings.Contains(rule, fmt.Sprintf("fwmark 0x%x", linuxRouterMark)) || !strings.Contains(rule, "lookup main") {
			return fmt.Errorf("policy priority %d is already owned by another rule", linuxRouterMarkRule)
		}
	}
	if rule, ok := seen[linuxRouterMainRule]; ok && !strings.Contains(rule, "lookup "+strconv.Itoa(linuxRouterTable)) {
		return fmt.Errorf("policy priority %d is already owned by another rule", linuxRouterMainRule)
	}
	routeOutput, err := runLinuxIPOutput("-4", "route", "show", "table", strconv.Itoa(linuxRouterTable))
	if err != nil {
		return fmt.Errorf("inspect Ethify routing table: %w", err)
	}
	for _, route := range strings.Split(strings.TrimSpace(routeOutput), "\n") {
		if route != "" && route != "default dev "+linuxRouterInterface &&
			!strings.HasPrefix(route, "default dev "+linuxRouterInterface+" ") {
			return fmt.Errorf("routing table %d contains a route not owned by Ethify: %s", linuxRouterTable, route)
		}
	}
	if len(seen) > 0 {
		if _, ok := seen[linuxRouterMainRule]; ok {
			if err := removeLinuxRouterRule(linuxRouterMainRule, "lookup "+strconv.Itoa(linuxRouterTable)); err != nil {
				return fmt.Errorf("remove stale Ethify policy rule: %w", err)
			}
		}
		if _, ok := seen[linuxRouterMarkRule]; ok {
			if err := removeLinuxRouterRule(linuxRouterMarkRule, fmt.Sprintf("fwmark 0x%x", linuxRouterMark)); err != nil {
				return fmt.Errorf("remove stale Ethify mark rule: %w", err)
			}
		}
		if err := runLinuxIP("-4", "route", "flush", "table", strconv.Itoa(linuxRouterTable)); err != nil {
			return fmt.Errorf("remove stale Ethify route: %w", err)
		}
	} else if err := runLinuxIP("-4", "route", "flush", "table", strconv.Itoa(linuxRouterTable)); err != nil {
		return fmt.Errorf("remove stale Ethify route: %w", err)
	}
	return nil
}

func runLinuxIP(args ...string) error {
	output, err := runLinuxIPOutput(args...)
	if err != nil {
		return fmt.Errorf("ip %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(output))
	}
	return nil
}

func runLinuxIPOutput(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
	return string(output), err
}
