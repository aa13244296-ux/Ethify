//go:build windows

package client

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
)

func TestAdapterDispatcherConcurrentSelectionAndReservations(t *testing.T) {
	networks := []DownloadNetwork{
		{InterfaceIndex: 6, Name: "Ethernet"},
		{InterfaceIndex: 13, Name: "Wi-Fi"},
	}
	dispatcher := &adapterDispatcher{
		networks:        networks,
		excludedIfIndex: 45,
		dialSlots:       make(chan struct{}, localRouterDialLimit),
		listNetworks: func() ([]DownloadNetwork, error) {
			return append([]DownloadNetwork(nil), networks...), nil
		},
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				candidates := dispatcher.candidates()
				if len(candidates) == 0 {
					continue
				}
				network := candidates[(worker+i)%len(candidates)]
				release, _ := dispatcher.reserve(network)
				release()
			}
		}(worker)
	}
	wg.Wait()
}

func TestAdapterDispatcherCapsConcurrentDials(t *testing.T) {
	const limit = 4
	dispatcher := &adapterDispatcher{dialSlots: make(chan struct{}, limit)}
	var active, maximum atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := dispatcher.acquireDial(context.Background()); err != nil {
				t.Errorf("acquire dial slot: %v", err)
				return
			}
			current := active.Add(1)
			for {
				prior := maximum.Load()
				if current <= prior || maximum.CompareAndSwap(prior, current) {
					break
				}
			}

			time.Sleep(time.Millisecond)
			active.Add(-1)
			dispatcher.releaseDial()
		}()
	}
	wg.Wait()
	if got := maximum.Load(); got > limit {
		t.Fatalf("concurrent outbound dials exceeded limit: got %d, limit %d", got, limit)
	}
	if got := maximum.Load(); got != limit {
		t.Fatalf("expected all dial slots to be used, got %d, limit %d", got, limit)
	}
}

func TestAdapterDispatcherKeepsSuccessfulEgressWithinGlobalLimit(t *testing.T) {
	const limit = 1
	dispatcher := &adapterDispatcher{
		networks:    []DownloadNetwork{{InterfaceIndex: 6, Name: "Ethernet", Address: "10.0.0.2"}},
		lastRefresh: time.Now(),
		dialSlots:   make(chan struct{}, limit),
	}
	remote, peer := net.Pipe()
	defer peer.Close()
	dispatcher.dialTCP = func(context.Context, DownloadNetwork, netip.AddrPort) (net.Conn, error) {
		return remote, nil
	}

	conn, err := dispatcher.DialContext(context.Background(), &M.Metadata{
		DstIP:   netip.MustParseAddr("203.0.113.1"),
		DstPort: 443,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := dispatcher.activeEgress.Load(); got != 1 || len(dispatcher.dialSlots) != limit {
		t.Fatalf("active egress=%d, reserved capacity=%d; want 1/%d", got, len(dispatcher.dialSlots), limit)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dispatcher.DialContext(ctx, &M.Metadata{
		DstIP: netip.MustParseAddr("203.0.113.2"),
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("dial at full capacity error = %v, want context cancellation", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if got := dispatcher.activeEgress.Load(); got != 0 || len(dispatcher.dialSlots) != 0 {
		t.Fatalf("egress capacity was not released on close: active=%d reserved=%d", got, len(dispatcher.dialSlots))
	}
}

func TestTrackedConnAccountsForBothDirections(t *testing.T) {
	conn, peer := net.Pipe()
	defer peer.Close()
	var transferred atomic.Uint64
	tracked := &trackedConn{
		Conn:        conn,
		recordBytes: func(n uint64) { transferred.Add(n) },
	}

	downloadDone := make(chan error, 1)
	go func() {
		_, err := peer.Write([]byte("download"))
		downloadDone <- err
	}()
	buffer := make([]byte, len("download"))
	if _, err := tracked.Read(buffer); err != nil {
		t.Fatal(err)
	}
	if err := <-downloadDone; err != nil {
		t.Fatal(err)
	}
	uploadDone := make(chan error, 1)
	go func() {
		_, err := peer.Read(make([]byte, len("upload")))
		uploadDone <- err
	}()
	if _, err := tracked.Write([]byte("upload")); err != nil {
		t.Fatal(err)
	}
	if err := <-uploadDone; err != nil {
		t.Fatal(err)
	}
	if got := transferred.Load(); got != uint64(len("download")+len("upload")) {
		t.Fatalf("tracked byte count=%d, want %d", got, len("download")+len("upload"))
	}
}

func TestIdleTimeoutConnAppliesReadDeadline(t *testing.T) {
	conn, peer := net.Pipe()
	defer conn.Close()
	defer peer.Close()
	idle := &idleTimeoutConn{Conn: conn, timeout: 10 * time.Millisecond}
	started := time.Now()
	_, err := idle.Read(make([]byte, 1))
	if networkErr, ok := err.(net.Error); !ok || !networkErr.Timeout() {
		t.Fatalf("idle read error = %v, want timeout", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("idle TCP timeout took longer than expected")
	}
}

func TestAdapterDispatcherRetriesAnotherAdapterAfterDialFailure(t *testing.T) {
	networks := []DownloadNetwork{
		{InterfaceIndex: 6, Name: "Ethernet", Address: "10.0.0.2"},
		{InterfaceIndex: 13, Name: "Wi-Fi", Address: "192.0.2.2"},
	}
	dispatcher := &adapterDispatcher{
		networks:    networks,
		dialSlots:   make(chan struct{}, 1),
		lastRefresh: time.Now(),
	}
	var tried []string
	peer, remote := net.Pipe()
	defer remote.Close()
	dispatcher.dialTCP = func(ctx context.Context, network DownloadNetwork, _ netip.AddrPort) (net.Conn, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("adapter dial has no timeout")
		}
		tried = append(tried, network.Name)
		if network.Name == "Ethernet" {
			return nil, context.DeadlineExceeded
		}
		return peer, nil
	}

	conn, err := dispatcher.DialContext(context.Background(), &M.Metadata{
		DstIP:   netip.MustParseAddr("203.0.113.1"),
		DstPort: 443,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if len(tried) != 2 || tried[0] != "Ethernet" || tried[1] != "Wi-Fi" {
		t.Fatalf("dial order = %v, want Ethernet then Wi-Fi", tried)
	}
	if got := dispatcher.tcpConnected.Load(); got != 1 {
		t.Fatalf("successful TCP dials = %d, want 1", got)
	}
}

func TestAdapterDispatcherFailsOverNewTCPAndUDPFlowsAfterAdapterDrop(t *testing.T) {
	networks := []DownloadNetwork{
		{InterfaceIndex: 6, Name: "Ethernet 2", Address: "10.0.0.2"},
		{InterfaceIndex: 13, Name: "Wi-Fi", Address: "192.0.2.2"},
	}
	available := append([]DownloadNetwork(nil), networks...)
	dispatcher := &adapterDispatcher{
		networks:    networks,
		lastRefresh: time.Now(),
		dialSlots:   make(chan struct{}, localRouterDialLimit),
		listNetworks: func() ([]DownloadNetwork, error) {
			return append([]DownloadNetwork(nil), available...), nil
		},
	}
	var triedTCP, triedUDP []string
	dispatcher.dialTCP = func(_ context.Context, network DownloadNetwork, _ netip.AddrPort) (net.Conn, error) {
		triedTCP = append(triedTCP, network.Name)
		if network.InterfaceIndex == 6 {
			return nil, errors.New("adapter was unplugged")
		}
		conn, peer := net.Pipe()
		_ = peer.Close()
		return conn, nil
	}
	dispatcher.dialUDP = func(_ context.Context, network DownloadNetwork) (net.PacketConn, error) {
		triedUDP = append(triedUDP, network.Name)
		if network.InterfaceIndex == 6 {
			return nil, errors.New("adapter was unplugged")
		}
		return net.ListenPacket("udp4", "127.0.0.1:0")
	}

	available = networks[1:]
	metadata := &M.Metadata{DstIP: netip.MustParseAddr("203.0.113.1"), DstPort: 443}
	tcpConn, err := dispatcher.DialContext(context.Background(), metadata)
	if err != nil {
		t.Fatalf("new TCP flow did not fail over: %v", err)
	}
	_ = tcpConn.Close()
	dispatcher.next = 0
	udpConn, err := dispatcher.DialUDP(metadata)
	if err != nil {
		t.Fatalf("new UDP flow did not fail over: %v", err)
	}
	_ = udpConn.Close()

	if len(triedTCP) == 0 || triedTCP[len(triedTCP)-1] != "Wi-Fi" {
		t.Fatalf("TCP flow did not finish on Wi-Fi after Ethernet dropped: %v", triedTCP)
	}
	if len(triedUDP) == 0 || triedUDP[len(triedUDP)-1] != "Wi-Fi" {
		t.Fatalf("UDP flow did not finish on Wi-Fi after Ethernet dropped: %v", triedUDP)
	}
	if got := dispatcher.tcpConnected.Load(); got != 1 {
		t.Fatalf("successful TCP flows = %d, want 1", got)
	}
	if got := dispatcher.udpConnected.Load(); got != 1 {
		t.Fatalf("successful UDP flows = %d, want 1", got)
	}
	dispatcher.lastRefresh = time.Now().Add(-3 * time.Second)
	if got := dispatcher.candidates(); len(got) != 1 || got[0].Name != "Wi-Fi" {
		t.Fatalf("active adapter set after refresh = %v, want the router on Wi-Fi", got)
	}
	router := &windowsLocalRouter{adapter: &WintunAdapter{}, dispatch: dispatcher}
	status := router.Status()
	if !status.Active {
		t.Fatal("router became inactive after adapter failover")
	}
	if len(status.Networks) != 1 || status.Networks[0].Name != "Wi-Fi" {
		t.Fatalf("router networks after failover = %v, want only Wi-Fi", status.Networks)
	}
	if status.TCPConnected != 1 || status.UDPConnected != 1 || status.LastError != "" {
		t.Fatalf("router status after failover = %+v, want successful TCP/UDP egress without errors", status)
	}
}

func TestAdapterDispatcherPrefersOnLinkAdapter(t *testing.T) {
	networks := []DownloadNetwork{
		{InterfaceIndex: 6, Name: "Wi-Fi", Address: "172.24.163.177", subnet: netip.MustParsePrefix("172.24.160.0/19")},
		{InterfaceIndex: 13, Name: "Ethernet", Address: "10.67.247.177", subnet: netip.MustParsePrefix("10.67.247.0/24")},
	}
	dispatcher := &adapterDispatcher{
		networks:    networks,
		lastRefresh: time.Now(),
	}
	ordered := dispatcher.candidatesForDestination(netip.MustParseAddr("10.67.247.36"))
	if len(ordered) != 2 || ordered[0].Name != "Ethernet" {
		t.Fatalf("on-link adapter order = %v, want Ethernet first", ordered)
	}
}

type testFlowCloser struct{ closed atomic.Int32 }

func (c *testFlowCloser) Close() error {
	c.closed.Add(1)
	return nil
}

func TestRecoverFlowPanicClosesFlow(t *testing.T) {
	conn := &testFlowCloser{}
	func() {
		defer recoverFlowPanic("test", conn)
		panic("simulated socket reset")
	}()
	if got := conn.closed.Load(); got != 1 {
		t.Fatalf("recovered flow close count = %d, want 1", got)
	}
}

func TestLocalTCPPipeStopsOnContextCancellation(t *testing.T) {
	origin, remote := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		localTCPPipe(ctx, origin, remote)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("TCP flow did not stop after context cancellation")
	}
}

func TestLocalUDPPipeStopsOnContextCancellation(t *testing.T) {
	origin, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	remote, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		_ = origin.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		localUDPPipe(ctx, origin, remote, remote.LocalAddr(), time.Minute)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("UDP flow did not stop after context cancellation")
	}
}

func TestAdapterDispatcherReportsDialFailures(t *testing.T) {
	dispatcher := &adapterDispatcher{dialSlots: make(chan struct{}, 1)}
	_, err := dispatcher.DialContext(context.Background(), &M.Metadata{
		DstIP:   netip.MustParseAddr("203.0.113.1"),
		DstPort: 443,
	})
	if err == nil {
		t.Fatal("expected dialing without networks to fail")
	}
	if got := dispatcher.flowError(); !strings.Contains(got, err.Error()) {
		t.Fatalf("reported flow error = %q, want it to contain %q", got, err)
	}
	if got := dispatcher.tcpDialAttempts.Load(); got != 1 {
		t.Fatalf("TCP dial attempts = %d, want 1", got)
	}
	if got := dispatcher.tcpConnected.Load(); got != 0 {
		t.Fatalf("successful TCP dials = %d, want 0", got)
	}
}
