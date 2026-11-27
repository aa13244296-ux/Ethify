//go:build windows

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/buffer"
	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"github.com/xjasonlyu/tun2socks/v2/core/device/iobased"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"golang.org/x/sys/windows"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	localRouterAdapterName     = "Ethifi Local Router"
	localRouterTunnelType      = "Ethifi"
	localRouterMTU             = 1500
	localRouterTCPWorkers      = localRouterEgressLimit
	localRouterUDPWorkers      = 32
	localRouterFlowQueue       = localRouterEgressLimit
	localRouterDialLimit       = localRouterEgressLimit
	localRouterDialTimeout     = 3 * time.Second
	localRouterAdapterCooldown = 4 * time.Second
	localRouterTCPKeepAlive    = 30 * time.Second
	localRouterTCPIdleTimeout  = 30 * time.Second
)

type windowsLocalRouter struct {
	mu       sync.Mutex
	adapter  *WintunAdapter
	stack    *stack.Stack
	flows    *localFlowHandler
	dispatch *adapterDispatcher
	routes   []string
	lastErr  string
	mutex    windows.Handle
	rateCaps map[int]uint64
}

func newLocalSystemRouter() localSystemRouter { return &windowsLocalRouter{} }

func (r *windowsLocalRouter) Enable() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapter != nil {
		if r.lastErr != "" {
			return errors.New(r.lastErr)
		}
		return nil
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("run Ethifi as Administrator to enable the local system router")
	}

	mutexName, err := syscall.UTF16PtrFromString(`Global\Ethifi.LocalRouter`)
	if err != nil {
		return fmt.Errorf("create local-router instance name: %w", err)
	}
	mutex, err := windows.CreateMutex(nil, false, mutexName)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(mutex)
		return errors.New("another Ethifi instance already owns the local router")
	}
	if err != nil {
		return fmt.Errorf("reserve local-router instance: %w", err)
	}
	r.mutex = mutex
	keepMutex := false
	defer func() {
		if !keepMutex {
			_ = windows.CloseHandle(r.mutex)
			r.mutex = 0
		}
	}()

	if err := cleanupStaleLocalRouterRoutes(); err != nil {
		return fmt.Errorf("clean up routes from a previous local-router session: %w", err)
	}

	networks, err := ListDownloadNetworks()
	if err != nil {
		return fmt.Errorf("find active network adapters: %w", err)
	}
	if len(networks) < 2 {
		return errors.New("connect at least two active IPv4 adapters before enabling the local router")
	}

	adapter, err := OpenOrCreateWintunAdapter(localRouterAdapterName, localRouterTunnelType, "")
	if err != nil {
		return fmt.Errorf("open local router adapter: %w", err)
	}
	cleanupAdapter := true
	defer func() {
		if cleanupAdapter {
			_ = adapter.Close()
		}
	}()

	routeManager := NewRouteManager()
	if err := routeManager.ConfigureAdapter(adapter.InterfaceIndex(), netip.MustParseAddr("10.77.0.1"), 24, localRouterMTU); err != nil {
		return fmt.Errorf("configure local router adapter: %w", err)
	}
	networks = withoutInterface(networks, int(adapter.InterfaceIndex()))
	if len(networks) < 2 {
		return errors.New("the local router adapter must be excluded and at least two physical IPv4 adapters must remain")
	}

	dispatcher := &adapterDispatcher{
		networks:        networks,
		excludedIfIndex: int(adapter.InterfaceIndex()),
		dialSlots:       make(chan struct{}, localRouterDialLimit),
	}
	dispatcher.setRateLimits(r.rateCaps)
	device, err := iobased.New(wintunReadWriter{adapter: adapter}, localRouterMTU, 0)
	if err != nil {
		return fmt.Errorf("create packet device: %w", err)
	}
	handler := newLocalFlowHandler(dispatcher)
	r.flows = handler
	tunnelStack, err := core.CreateStack(&core.Config{
		LinkEndpoint:     device,
		TransportHandler: handler,
	})
	if err != nil {
		handler.Close()
		r.flows = nil
		return fmt.Errorf("create local TCP/IP stack: %w", err)
	}

	addRoute := func(prefix string, ifIndex uint32) error {
		return runNetsh(fmt.Sprintf("interface ipv4 add route prefix=%s interface=%d metric=5 store=active", prefix, ifIndex))
	}
	deleteRoute := func(prefix string, ifIndex uint32) error {
		return runNetsh(fmt.Sprintf("interface ipv4 delete route prefix=%s interface=%d store=active", prefix, ifIndex))
	}
	installed, err := installLocalDefaultRoutes(adapter.InterfaceIndex(), addRoute, deleteRoute)
	if err != nil {
		if len(installed) > 0 {
			r.adapter = adapter
			r.stack = tunnelStack
			r.flows = handler
			r.dispatch = dispatcher
			r.routes = installed
			r.lastErr = err.Error()
			cleanupAdapter = false
			keepMutex = true
		} else {
			handler.Close()
			tunnelStack.Close()
		}
		return fmt.Errorf("install local system routes: %w", err)
	}

	r.adapter = adapter
	r.stack = tunnelStack
	r.flows = handler
	r.dispatch = dispatcher
	r.routes = installed
	r.lastErr = ""
	cleanupAdapter = false
	keepMutex = true
	log.Printf("[Local Router] Enabled on Wintun interface %d across %d adapters", adapter.InterfaceIndex(), len(networks))
	return nil
}

func (r *windowsLocalRouter) Disable() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapter == nil {
		return nil
	}

	remaining := make([]string, 0, len(r.routes))
	var routeErrors []error
	for _, prefix := range r.routes {
		if err := runNetsh(fmt.Sprintf("interface ipv4 delete route prefix=%s interface=%d store=active", prefix, r.adapter.InterfaceIndex())); err != nil {
			remaining = append(remaining, prefix)
			routeErrors = append(routeErrors, fmt.Errorf("remove local system route %s: %w", prefix, err))
		}
	}
	r.routes = remaining
	if err := errors.Join(routeErrors...); err != nil {
		r.lastErr = err.Error()
		return err
	}
	r.routes = nil
	r.flows.Close()
	r.stack.Close()
	r.flows = nil
	if err := r.adapter.Close(); err != nil {
		r.lastErr = fmt.Sprintf("close local router adapter: %v", err)
		return errors.New(r.lastErr)
	}
	r.adapter = nil
	r.stack = nil
	r.flows = nil
	r.dispatch = nil
	r.lastErr = ""
	if r.mutex != 0 {
		if err := windows.CloseHandle(r.mutex); err != nil {
			r.lastErr = fmt.Sprintf("release local-router instance lock: %v", err)
			return errors.New(r.lastErr)
		}
		r.mutex = 0
	}
	return nil
}

func (r *windowsLocalRouter) SetAdapterRateLimits(limits map[int]uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rateCaps = make(map[int]uint64, len(limits))
	for index, limit := range limits {
		r.rateCaps[index] = limit
	}
	if r.dispatch != nil {
		r.dispatch.setRateLimits(r.rateCaps)
	}
	return nil
}

func (r *windowsLocalRouter) Status() localRouterStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := localRouterStatus{
		Active:    r.adapter != nil,
		Available: true,
		Elevated:  windows.GetCurrentProcessToken().IsElevated(),
		Platform:  "windows",
		Networks:  []DownloadNetwork{},
		LastError: r.lastErr,
	}
	if r.dispatch != nil {
		status.Networks = r.dispatch.snapshot()
		status.AdapterLoads = r.dispatch.loadSnapshot()
		status.ActiveEgress = r.dispatch.activeEgress.Load()
		status.EgressLimit = localRouterEgressLimit
		status.TCPDialAttempts = r.dispatch.tcpDialAttempts.Load()
		status.UDPDialAttempts = r.dispatch.udpDialAttempts.Load()
		status.TCPConnected = r.dispatch.tcpConnected.Load()
		status.UDPConnected = r.dispatch.udpConnected.Load()
		if status.LastError == "" {
			status.LastError = r.dispatch.flowError()
		}
	}
	if r.flows != nil {
		status.TCPFlows = r.flows.tcpFlows.Load()
		status.UDPFlows = r.flows.udpFlows.Load()
	}
	return status
}

func (r *windowsLocalRouter) Close() error { return r.Disable() }

func withoutInterface(networks []DownloadNetwork, ifIndex int) []DownloadNetwork {
	filtered := make([]DownloadNetwork, 0, len(networks))
	for _, network := range networks {
		if network.InterfaceIndex != ifIndex {
			filtered = append(filtered, network)
		}
	}
	return filtered
}

type wintunReadWriter struct{ adapter *WintunAdapter }

func (rw wintunReadWriter) Read(packet []byte) (int, error) {
	return rw.adapter.ReadPacket(packet)
}

func (rw wintunReadWriter) Write(packet []byte) (int, error) {
	if err := rw.adapter.WritePacket(packet); err != nil {
		return 0, err
	}
	return len(packet), nil
}

type adapterDispatcher struct {
	mu              sync.Mutex
	networks        []DownloadNetwork
	excludedIfIndex int
	active          map[int]int
	byteCounters    map[int]*atomic.Uint64
	byteSamples     map[int]adapterByteSample
	rateLimiters    map[int]*adapterRateLimiter
	failedAt        map[int]time.Time
	lastTCPError    string
	lastUDPError    string
	activeEgress    atomic.Uint64
	tcpDialAttempts atomic.Uint64
	udpDialAttempts atomic.Uint64
	tcpConnected    atomic.Uint64
	udpConnected    atomic.Uint64
	next            int
	dialSlots       chan struct{}
	dialerPool      sync.Pool
	dialTCP         func(context.Context, DownloadNetwork, netip.AddrPort) (net.Conn, error)
	dialUDP         func(context.Context, DownloadNetwork) (net.PacketConn, error)
	lastRefresh     time.Time
	listNetworks    func() ([]DownloadNetwork, error)
}

type adapterByteSample struct {
	bytesPerSecond float64
	sampledBytes   uint64
	sampledAt      time.Time
}

type adapterRateLimiter struct {
	bytesPerSecond atomic.Uint64
	pacer          bytePacer
}

type bytePacer struct {
	mu   sync.Mutex
	next time.Time
}

func (p *bytePacer) wait(rate uint64, bytes int, done <-chan struct{}) bool {
	if rate == 0 || bytes <= 0 {
		return true
	}
	duration := time.Duration(float64(bytes) / float64(rate) * float64(time.Second))
	if duration <= 0 {
		return true
	}
	p.mu.Lock()
	now := time.Now()
	start := p.next
	if start.Before(now) {
		start = now
	}
	p.next = start.Add(duration)
	p.mu.Unlock()
	if !start.After(now) {
		return true
	}
	timer := time.NewTimer(time.Until(start))
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-done:
		return false
	}
}

func (l *adapterRateLimiter) chunkSize(requested int) int {
	rate := l.bytesPerSecond.Load()
	if rate == 0 || requested <= 0 {
		return requested
	}
	chunk := int(rate / 10)
	if chunk < 1 {
		chunk = 1
	}
	if chunk > 16*1024 {
		chunk = 16 * 1024
	}
	if chunk > requested {
		chunk = requested
	}
	return chunk
}

func (l *adapterRateLimiter) waitDownload(bytes int, done <-chan struct{}) bool {
	return l.pacer.wait(l.bytesPerSecond.Load(), bytes, done)
}

func (l *adapterRateLimiter) waitUpload(bytes int, done <-chan struct{}) bool {
	return l.pacer.wait(l.bytesPerSecond.Load(), bytes, done)
}

func (d *adapterDispatcher) setRateLimits(limits map[int]uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rateLimiters == nil {
		d.rateLimiters = make(map[int]*adapterRateLimiter, len(limits))
	}
	for index, limiter := range d.rateLimiters {
		if _, ok := limits[index]; !ok {
			limiter.bytesPerSecond.Store(0)
		}
	}
	for index, limit := range limits {
		if index <= 0 || limit == 0 {
			continue
		}
		limiter := d.rateLimiters[index]
		if limiter == nil {
			limiter = &adapterRateLimiter{}
			d.rateLimiters[index] = limiter
		}
		limiter.bytesPerSecond.Store(limit)
	}
}

func (d *adapterDispatcher) snapshot() []DownloadNetwork {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]DownloadNetwork(nil), d.networks...)
}

func (d *adapterDispatcher) loadSnapshot() []localRouterAdapterLoad {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	rates := d.sampleAdapterRatesLocked(now)
	loads := make([]localRouterAdapterLoad, 0, len(d.networks))
	for _, network := range d.networks {
		loads = append(loads, localRouterAdapterLoad{
			InterfaceIndex: network.InterfaceIndex,
			ActiveFlows:    d.active[network.InterfaceIndex],
			BytesPerSecond: uint64(rates[network.InterfaceIndex]),
		})
	}
	return loads
}

func (d *adapterDispatcher) candidates() []DownloadNetwork {
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Since(d.lastRefresh) >= 2*time.Second {
		listNetworks := d.listNetworks
		if listNetworks == nil {
			listNetworks = ListDownloadNetworks
		}
		current, err := listNetworks()
		if err == nil {
			d.networks = d.networks[:0]
			for _, network := range current {
				if network.InterfaceIndex != d.excludedIfIndex {
					d.networks = append(d.networks, network)
				}
			}
			d.lastRefresh = time.Now()
		}
	}
	if len(d.networks) == 0 {
		return nil
	}
	if d.active == nil {
		d.active = make(map[int]int)
	}
	now := time.Now()
	ordered := orderAdapterCandidatesWithCooldown(
		d.networks, d.active, d.sampleAdapterRatesLocked(now),
		d.failedAt, now, localRouterAdapterCooldown, d.next,
	)
	d.next = (d.next + 1) % len(d.networks)
	return ordered
}

func (d *adapterDispatcher) sampleAdapterRatesLocked(now time.Time) map[int]float64 {
	if d.byteCounters == nil {
		d.byteCounters = make(map[int]*atomic.Uint64)
	}
	if d.byteSamples == nil {
		d.byteSamples = make(map[int]adapterByteSample)
	}
	rates := make(map[int]float64, len(d.networks))
	for _, network := range d.networks {
		counter := d.byteCounters[network.InterfaceIndex]
		if counter == nil {
			counter = &atomic.Uint64{}
			d.byteCounters[network.InterfaceIndex] = counter
		}
		sample, exists := d.byteSamples[network.InterfaceIndex]
		if !exists {
			sample.sampledAt = now
		} else if elapsed := now.Sub(sample.sampledAt); elapsed >= 250*time.Millisecond {
			current := counter.Load()
			sample.bytesPerSecond = float64(current-sample.sampledBytes) / elapsed.Seconds()
			sample.sampledBytes = current
			sample.sampledAt = now
		}
		d.byteSamples[network.InterfaceIndex] = sample
		rates[network.InterfaceIndex] = sample.bytesPerSecond
	}
	return rates
}

func (d *adapterDispatcher) candidatesForDestination(destination netip.Addr) []DownloadNetwork {
	ordered := d.candidates()
	if !destination.Is4() || len(ordered) < 2 {
		return ordered
	}
	local := make([]DownloadNetwork, 0, len(ordered))
	other := make([]DownloadNetwork, 0, len(ordered))
	for _, network := range ordered {
		if network.subnet.IsValid() && network.subnet.Contains(destination) {
			local = append(local, network)
		} else {
			other = append(other, network)
		}
	}
	return append(local, other...)
}

func (d *adapterDispatcher) reserve(network DownloadNetwork) (func(), *atomic.Uint64, *adapterRateLimiter) {
	d.mu.Lock()
	if d.active == nil {
		d.active = make(map[int]int)
	}
	if d.byteCounters == nil {
		d.byteCounters = make(map[int]*atomic.Uint64)
	}
	counter := d.byteCounters[network.InterfaceIndex]
	if counter == nil {
		counter = &atomic.Uint64{}
		d.byteCounters[network.InterfaceIndex] = counter
	}
	d.active[network.InterfaceIndex]++
	limiter := d.rateLimiters[network.InterfaceIndex]
	d.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			d.active[network.InterfaceIndex]--
			d.mu.Unlock()
		})
	}, counter, limiter
}

func (d *adapterDispatcher) reportAdapterResult(network DownloadNetwork, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err == nil {
		delete(d.failedAt, network.InterfaceIndex)
		return
	}
	if d.failedAt == nil {
		d.failedAt = make(map[int]time.Time)
	}
	d.failedAt[network.InterfaceIndex] = time.Now()
}

func (d *adapterDispatcher) acquireDial(ctx context.Context) error {
	if d.dialSlots == nil {
		return errors.New("outbound dial limiter is not initialized")
	}
	select {
	case d.dialSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *adapterDispatcher) releaseDial() { <-d.dialSlots }

func (d *adapterDispatcher) reportTCPError(err error) {
	d.mu.Lock()
	if err == nil {
		d.lastTCPError = ""
	} else {
		d.lastTCPError = err.Error()
	}
	d.mu.Unlock()
}

func (d *adapterDispatcher) reportUDPError(err error) {
	d.mu.Lock()
	if err == nil {
		d.lastUDPError = ""
	} else {
		d.lastUDPError = err.Error()
	}
	d.mu.Unlock()
}

func (d *adapterDispatcher) flowError() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var messages []string
	if d.lastTCPError != "" {
		messages = append(messages, "TCP: "+d.lastTCPError)
	}
	if d.lastUDPError != "" {
		messages = append(messages, "UDP: "+d.lastUDPError)
	}
	return strings.Join(messages, "; ")
}

func (d *adapterDispatcher) dialer() *net.Dialer {
	if pooled := d.dialerPool.Get(); pooled != nil {
		return pooled.(*net.Dialer)
	}
	return &net.Dialer{}
}

func (d *adapterDispatcher) putDialer(dialer *net.Dialer) {
	*dialer = net.Dialer{}
	d.dialerPool.Put(dialer)
}

func (d *adapterDispatcher) DialContext(ctx context.Context, metadata *M.Metadata) (net.Conn, error) {
	d.tcpDialAttempts.Add(1)
	if metadata == nil || !metadata.DstIP.Is4() {
		err := errors.New("local router currently forwards IPv4 TCP flows only")
		d.reportTCPError(err)
		return nil, err
	}
	if err := d.acquireDial(ctx); err != nil {
		err = fmt.Errorf("wait for an outbound dial slot: %w", err)
		d.reportTCPError(err)
		return nil, err
	}
	slotTransferred := false
	defer func() {
		if !slotTransferred {
			d.releaseDial()
		}
	}()

	var errs []error
	for _, network := range d.candidatesForDestination(metadata.DstIP) {
		ip, err := netip.ParseAddr(network.Address)
		if err != nil || !ip.Is4() {
			continue
		}
		release, byteCounter, rateLimiter := d.reserve(network)
		dialCtx, cancel := context.WithTimeout(ctx, localRouterDialTimeout)
		dial := d.dialTCP
		if dial == nil {
			dial = d.dialPhysicalTCP
		}
		conn, err := dial(dialCtx, network, metadata.DestinationAddrPort())
		cancel()
		if err != nil {
			d.reportAdapterResult(network, err)
			release()
			errs = append(errs, fmt.Errorf("%s: %w", network.Name, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		d.reportAdapterResult(network, nil)
		d.reportTCPError(nil)
		d.tcpConnected.Add(1)
		d.activeEgress.Add(1)
		slotTransferred = true
		return &trackedConn{
			Conn: &idleTimeoutConn{
				Conn:    conn,
				timeout: localRouterTCPIdleTimeout,
			},
			recordBytes: func(n uint64) { byteCounter.Add(n) },
			rateLimiter: rateLimiter,
			done:        make(chan struct{}),
			release: func() {
				release()
				d.activeEgress.Add(^uint64(0))
				d.releaseDial()
			},
		}, nil
	}
	err := errors.Join(append([]error{errors.New("no active physical adapter could open the TCP flow")}, errs...)...)
	d.reportTCPError(err)
	return nil, err
}

func (d *adapterDispatcher) dialPhysicalTCP(ctx context.Context, network DownloadNetwork, destination netip.AddrPort) (net.Conn, error) {
	ip, err := netip.ParseAddr(network.Address)
	if err != nil || !ip.Is4() {
		return nil, fmt.Errorf("adapter %s has an invalid IPv4 address", network.Name)
	}
	dialer := d.dialer()
	*dialer = net.Dialer{
		Timeout:   localRouterDialTimeout,
		KeepAlive: localRouterTCPKeepAlive,
		LocalAddr: &net.TCPAddr{IP: net.IP(ip.AsSlice())},
		Control: func(_, _ string, raw syscall.RawConn) error {
			return bindTCPInterface(raw, network.InterfaceIndex)
		},
	}
	conn, err := dialer.DialContext(ctx, "tcp4", destination.String())
	d.putDialer(dialer)
	return conn, err
}

func (d *adapterDispatcher) DialUDP(metadata *M.Metadata) (net.PacketConn, error) {
	d.udpDialAttempts.Add(1)
	if metadata == nil || !metadata.DstIP.Is4() {
		err := errors.New("local router currently forwards IPv4 UDP flows only")
		d.reportUDPError(err)
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), localRouterDialTimeout)
	defer cancel()
	if err := d.acquireDial(ctx); err != nil {
		err = fmt.Errorf("wait for an outbound dial slot: %w", err)
		d.reportUDPError(err)
		return nil, err
	}
	slotTransferred := false
	defer func() {
		if !slotTransferred {
			d.releaseDial()
		}
	}()

	var errs []error
	for _, network := range d.candidatesForDestination(metadata.DstIP) {
		ip, err := netip.ParseAddr(network.Address)
		if err != nil || !ip.Is4() {
			continue
		}
		release, byteCounter, rateLimiter := d.reserve(network)
		dialCtx, cancel := context.WithTimeout(ctx, localRouterDialTimeout)
		dial := d.dialUDP
		if dial == nil {
			dial = d.dialPhysicalUDP
		}
		packetConn, err := dial(dialCtx, network)
		cancel()
		if err != nil {
			d.reportAdapterResult(network, err)
			release()
			errs = append(errs, fmt.Errorf("%s: %w", network.Name, err))
			continue
		}
		d.reportAdapterResult(network, nil)
		d.reportUDPError(nil)
		d.udpConnected.Add(1)
		d.activeEgress.Add(1)
		slotTransferred = true
		return &trackedPacketConn{
			PacketConn:  packetConn,
			recordBytes: func(n uint64) { byteCounter.Add(n) },
			rateLimiter: rateLimiter,
			done:        make(chan struct{}),
			release: func() {
				release()
				d.activeEgress.Add(^uint64(0))
				d.releaseDial()
			},
		}, nil
	}
	err := errors.Join(append([]error{errors.New("no active physical adapter could open the UDP flow")}, errs...)...)
	d.reportUDPError(err)
	return nil, err
}

func (d *adapterDispatcher) dialPhysicalUDP(ctx context.Context, network DownloadNetwork) (net.PacketConn, error) {
	listenConfig := net.ListenConfig{
		Control: func(_, _ string, raw syscall.RawConn) error {
			return bindTCPInterface(raw, network.InterfaceIndex)
		},
	}
	return listenConfig.ListenPacket(ctx, "udp4", net.JoinHostPort(network.Address, "0"))
}

type localFlowHandler struct {
	dispatcher *adapterDispatcher
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	closed     bool
	tcpQueue   chan adapter.TCPConn
	udpQueue   chan adapter.UDPConn
	tcpFlows   atomic.Uint64
	udpFlows   atomic.Uint64
	wg         sync.WaitGroup
	closeOnce  sync.Once
}

func newLocalFlowHandler(dispatcher *adapterDispatcher) *localFlowHandler {
	ctx, cancel := context.WithCancel(context.Background())
	handler := &localFlowHandler{
		dispatcher: dispatcher,
		ctx:        ctx,
		cancel:     cancel,
		tcpQueue:   make(chan adapter.TCPConn, localRouterFlowQueue),
		udpQueue:   make(chan adapter.UDPConn, localRouterFlowQueue),
	}
	for i := 0; i < localRouterTCPWorkers; i++ {
		handler.wg.Add(1)
		go handler.runTCPWorker()
	}
	for i := 0; i < localRouterUDPWorkers; i++ {
		handler.wg.Add(1)
		go handler.runUDPWorker()
	}
	return handler
}

func (h *localFlowHandler) HandleTCP(conn adapter.TCPConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		_ = conn.Close()
		return
	}
	select {
	case h.tcpQueue <- conn:
		h.tcpFlows.Add(1)
	default:
		log.Printf("[Local Router] TCP flow queue is full; rejecting new flow")
		_ = conn.Close()
	}
}

func (h *localFlowHandler) HandleUDP(conn adapter.UDPConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		_ = conn.Close()
		return
	}
	select {
	case h.udpQueue <- conn:
		h.udpFlows.Add(1)
	default:
		log.Printf("[Local Router] UDP flow queue is full; rejecting new flow")
		_ = conn.Close()
	}
}

func (h *localFlowHandler) runTCPWorker() {
	defer h.wg.Done()
	for {
		select {
		case <-h.ctx.Done():
			return
		case conn := <-h.tcpQueue:
			h.handleTCP(conn)
		}
	}
}

func (h *localFlowHandler) runUDPWorker() {
	defer h.wg.Done()
	for {
		select {
		case <-h.ctx.Done():
			return
		case conn := <-h.udpQueue:
			h.handleUDP(conn)
		}
	}
}

func (h *localFlowHandler) handleTCP(conn adapter.TCPConn) {
	defer recoverFlowPanic("TCP", conn)
	defer conn.Close()

	id := conn.ID()
	metadata := &M.Metadata{
		Network: M.TCP,
		SrcIP:   ipAddressFromTCP(id.RemoteAddress),
		SrcPort: id.RemotePort,
		DstIP:   ipAddressFromTCP(id.LocalAddress),
		DstPort: id.LocalPort,
	}
	ctx, cancel := context.WithTimeout(h.ctx, localRouterDialTimeout)
	defer cancel()
	remote, err := h.dispatcher.DialContext(ctx, metadata)
	if err != nil {
		log.Printf("[Local Router] TCP dial %s failed: %v", metadata.DestinationAddress(), err)
		return
	}
	defer remote.Close()
	localTCPPipe(h.ctx, conn, remote)
}

func (h *localFlowHandler) handleUDP(conn adapter.UDPConn) {
	defer recoverFlowPanic("UDP", conn)
	defer conn.Close()

	id := conn.ID()
	metadata := &M.Metadata{
		Network: M.UDP,
		SrcIP:   ipAddressFromTCP(id.RemoteAddress),
		SrcPort: id.RemotePort,
		DstIP:   ipAddressFromTCP(id.LocalAddress),
		DstPort: id.LocalPort,
	}
	remote, err := h.dispatcher.DialUDP(metadata)
	if err != nil {
		log.Printf("[Local Router] UDP dial %s failed: %v", metadata.DestinationAddress(), err)
		return
	}
	defer remote.Close()
	destination := metadata.UDPAddr()
	if destination == nil {
		log.Printf("[Local Router] UDP flow has no valid IPv4 destination: %s", metadata.DestinationAddress())
		return
	}
	localUDPPipe(h.ctx, conn, &localSymmetricPacketConn{
		PacketConn:  remote,
		destination: metadata.DestinationAddress(),
	}, destination, localRouterUDPTimeout)
}

func (h *localFlowHandler) Close() {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closed = true
		h.mu.Unlock()
		h.cancel()
		h.wg.Wait()
		for {
			select {
			case conn := <-h.tcpQueue:
				_ = conn.Close()
			case conn := <-h.udpQueue:
				_ = conn.Close()
			default:
				return
			}
		}
	})
}

const localRouterUDPTimeout = 60 * time.Second

func recoverFlowPanic(protocol string, conn interface{ Close() error }) {
	if recovered := recover(); recovered != nil {
		log.Printf("[Local Router] Recovered %s flow panic: %v", protocol, recovered)
		_ = conn.Close()
	}
}

func localTCPPipe(ctx context.Context, origin, remote net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go localTCPStream(remote, origin, "origin->remote", &wg)
	go localTCPStream(origin, remote, "remote->origin", &wg)
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

func localTCPStream(dst, src net.Conn, direction string, wg *sync.WaitGroup) {
	defer wg.Done()
	defer recoverFlowPanic("TCP stream", dst)
	buf := buffer.Get(buffer.RelayBufferSize)
	defer buffer.Put(buf)
	if _, err := io.CopyBuffer(dst, src, buf); err != nil {
		log.Printf("[Local Router] TCP copy %s failed: %v", direction, err)
	}
	if halfCloser, ok := src.(interface{ CloseRead() error }); ok {
		_ = halfCloser.CloseRead()
	}
	if halfCloser, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = halfCloser.CloseWrite()
	}
	_ = dst.SetReadDeadline(time.Now().Add(time.Minute))
}

func localUDPPipe(ctx context.Context, origin, remote net.PacketConn, destination net.Addr, timeout time.Duration) {
	var wg sync.WaitGroup
	wg.Add(2)
	go localUDPStream(remote, origin, destination, "origin->remote", &wg, timeout)
	go localUDPStream(origin, remote, nil, "remote->origin", &wg, timeout)
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

func localUDPStream(dst, src net.PacketConn, destination net.Addr, direction string, wg *sync.WaitGroup, timeout time.Duration) {
	defer wg.Done()
	defer recoverFlowPanic("UDP stream", dst)
	packet := localUDPPacketPool.Get().([]byte)
	defer localUDPPacketPool.Put(packet)
	for {
		if err := src.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			log.Printf("[Local Router] UDP deadline %s failed: %v", direction, err)
			return
		}
		n, _, err := src.ReadFrom(packet)
		if networkErr, ok := err.(net.Error); ok && networkErr.Timeout() || err == io.EOF {
			return
		}
		if err != nil {
			log.Printf("[Local Router] UDP read %s failed: %v", direction, err)
			return
		}
		if destination != nil {
			_, err = dst.WriteTo(packet[:n], destination)
		} else {
			_, err = dst.WriteTo(packet[:n], nil)
		}
		if err != nil {
			log.Printf("[Local Router] UDP write %s failed: %v", direction, err)
			return
		}
	}
}

var localUDPPacketPool = sync.Pool{
	New: func() any { return make([]byte, 65535) },
}

type localSymmetricPacketConn struct {
	net.PacketConn
	destination string
}

func (pc *localSymmetricPacketConn) ReadFrom(packet []byte) (int, net.Addr, error) {
	for {
		n, from, err := pc.PacketConn.ReadFrom(packet)
		if err != nil || from == nil || from.String() == pc.destination {
			return n, from, err
		}
		log.Printf("[Local Router] Dropped UDP response from unexpected peer %s (expected %s)", from, pc.destination)
	}
}

func ipAddressFromTCP(address tcpip.Address) netip.Addr {
	ip, _ := netip.AddrFromSlice(address.AsSlice())
	return ip
}

type trackedConn struct {
	net.Conn
	recordBytes func(uint64)
	rateLimiter *adapterRateLimiter
	done        chan struct{}
	release     func()
	once        sync.Once
}

func (c *trackedConn) Read(buffer []byte) (int, error) {
	readBuffer := buffer
	if c.rateLimiter != nil {
		readBuffer = buffer[:c.rateLimiter.chunkSize(len(buffer))]
	}
	n, err := c.Conn.Read(readBuffer)
	if n > 0 {
		if c.rateLimiter != nil && !c.rateLimiter.waitDownload(n, c.done) {
			return 0, net.ErrClosed
		}
		if c.recordBytes != nil {
			c.recordBytes(uint64(n))
		}
	}
	return n, err
}

func (c *trackedConn) Write(buffer []byte) (int, error) {
	if c.rateLimiter == nil {
		n, err := c.Conn.Write(buffer)
		if n > 0 && c.recordBytes != nil {
			c.recordBytes(uint64(n))
		}
		return n, err
	}
	written := 0
	for len(buffer) > 0 {
		chunkSize := c.rateLimiter.chunkSize(len(buffer))
		chunk := buffer[:chunkSize]
		if !c.rateLimiter.waitUpload(len(chunk), c.done) {
			return written, net.ErrClosed
		}
		n, err := c.Conn.Write(chunk)
		if n > 0 {
			written += n
			buffer = buffer[n:]
			if c.recordBytes != nil {
				c.recordBytes(uint64(n))
			}
		}
		if err != nil {
			return written, err
		}
		if n != len(chunk) {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		if c.done != nil {
			close(c.done)
		}
		if c.release != nil {
			c.release()
		}
	})
	return err
}

type trackedPacketConn struct {
	net.PacketConn
	recordBytes func(uint64)
	rateLimiter *adapterRateLimiter
	done        chan struct{}
	release     func()
	once        sync.Once
}

func (c *trackedPacketConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	n, address, err := c.PacketConn.ReadFrom(buffer)
	if n > 0 && c.rateLimiter != nil && !c.rateLimiter.waitDownload(n, c.done) {
		return 0, nil, net.ErrClosed
	}
	if n > 0 && c.recordBytes != nil {
		c.recordBytes(uint64(n))
	}
	return n, address, err
}

func (c *trackedPacketConn) WriteTo(buffer []byte, address net.Addr) (int, error) {
	if c.rateLimiter != nil && !c.rateLimiter.waitUpload(len(buffer), c.done) {
		return 0, net.ErrClosed
	}
	n, err := c.PacketConn.WriteTo(buffer, address)
	if n > 0 && c.recordBytes != nil {
		c.recordBytes(uint64(n))
	}
	return n, err
}

func (c *trackedPacketConn) Close() error {
	err := c.PacketConn.Close()
	c.once.Do(func() {
		if c.done != nil {
			close(c.done)
		}
		if c.release != nil {
			c.release()
		}
	})
	return err
}

type idleTimeoutConn struct {
	net.Conn
	timeout time.Duration
}

func (c *idleTimeoutConn) Read(buffer []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(buffer)
}

func (c *idleTimeoutConn) Write(buffer []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(buffer)
}

var _ proxy.Proxy = (*adapterDispatcher)(nil)
var _ adapter.TransportHandler = (*localFlowHandler)(nil)
