package client

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	localSOCKSAddress = "127.0.0.1:1080"
	localHTTPAddress  = "127.0.0.1:8080"
	proxyFailureHold  = 10 * time.Second
)

type proxyDialFunc func(context.Context, DownloadNetwork, string) (net.Conn, error)

type localProxyAdapter struct {
	network DownloadNetwork

	activeConnections atomic.Int64
	bytesUp           atomic.Int64
	bytesDown         atomic.Int64
	failures          atomic.Int64
	unhealthyUntil    atomic.Int64
	lastError         atomic.Pointer[string]

	sampleMu sync.Mutex
	lastUp   int64
	lastDown int64
	lastAt   time.Time
	upRate   int64
	downRate int64
}

type LocalProxyAdapterSnapshot struct {
	InterfaceIndex    int    `json:"interfaceIndex"`
	Name              string `json:"name"`
	Address           string `json:"address"`
	ActiveConnections int64  `json:"activeConnections"`
	BytesUp           int64  `json:"bytesUp"`
	BytesDown         int64  `json:"bytesDown"`
	UpRateBps         int64  `json:"upRateBps"`
	DownRateBps       int64  `json:"downRateBps"`
	Failures          int64  `json:"failures"`
	Healthy           bool   `json:"healthy"`
	LastError         string `json:"lastError,omitempty"`
}

type LocalProxySnapshot struct {
	Running      bool                        `json:"running"`
	SOCKSAddress string                      `json:"socksAddress"`
	HTTPAddress  string                      `json:"httpAddress"`
	Adapters     []LocalProxyAdapterSnapshot `json:"adapters"`
}

type LocalProxyManager struct {
	opMu sync.Mutex
	mu   sync.Mutex

	running  bool
	adapters []*localProxyAdapter
	byIndex  map[int]*localProxyAdapter

	socksListener net.Listener
	httpListener  net.Listener
	socksAddress  string
	httpAddress   string
	activeConns   map[net.Conn]struct{}
	nextAdapter   atomic.Uint32

	acceptWG sync.WaitGroup
	connWG   sync.WaitGroup
	dial     proxyDialFunc
}

func NewLocalProxyManager() *LocalProxyManager {
	return &LocalProxyManager{
		byIndex:     make(map[int]*localProxyAdapter),
		activeConns: make(map[net.Conn]struct{}),
		dial:        dialProxyAdapter,
	}
}

func (p *LocalProxyManager) Start(interfaceIndices []int) error {
	available, err := ListDownloadNetworks()
	if err != nil {
		return err
	}
	wanted := make(map[int]struct{}, len(interfaceIndices))
	for _, index := range interfaceIndices {
		if index <= 0 {
			return fmt.Errorf("invalid network interface index %d", index)
		}
		if _, exists := wanted[index]; exists {
			return fmt.Errorf("network interface %d is selected more than once", index)
		}
		wanted[index] = struct{}{}
	}

	selected := make([]DownloadNetwork, 0, len(wanted))
	for _, network := range available {
		if _, ok := wanted[network.InterfaceIndex]; ok {
			selected = append(selected, network)
			delete(wanted, network.InterfaceIndex)
		}
	}
	if len(wanted) != 0 {
		return errors.New("one or more selected networks are no longer active")
	}
	return p.startNetworks(selected, localSOCKSAddress, localHTTPAddress)
}

func (p *LocalProxyManager) startNetworks(networks []DownloadNetwork, socksAddress, httpAddress string) error {
	p.opMu.Lock()
	defer p.opMu.Unlock()

	p.mu.Lock()
	running := p.running
	p.mu.Unlock()
	minimumAdapters := 2
	if running {
		minimumAdapters = 1
	}
	if err := validateProxyNetworksMinimum(networks, minimumAdapters); err != nil {
		return err
	}

	p.mu.Lock()
	if p.running {
		p.setNetworksLocked(networks)
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	socksListener, err := net.Listen("tcp", socksAddress)
	if err != nil {
		return fmt.Errorf("listen for local SOCKS5 proxy on %s: %w", socksAddress, err)
	}
	httpListener, err := net.Listen("tcp", httpAddress)
	if err != nil {
		_ = socksListener.Close()
		return fmt.Errorf("listen for local HTTP proxy on %s: %w", httpAddress, err)
	}

	p.mu.Lock()
	p.setNetworksLocked(networks)
	p.socksListener = socksListener
	p.httpListener = httpListener
	p.socksAddress = socksListener.Addr().String()
	p.httpAddress = httpListener.Addr().String()
	p.running = true
	p.acceptWG.Add(2)
	p.mu.Unlock()

	go p.acceptLoop(socksListener, true)
	go p.acceptLoop(httpListener, false)
	return nil
}

func validateProxyNetworks(networks []DownloadNetwork) error {
	return validateProxyNetworksMinimum(networks, 2)
}

func validateProxyNetworksMinimum(networks []DownloadNetwork, minimum int) error {
	if len(networks) < minimum {
		return fmt.Errorf("select at least %d active adapters for multi-adapter proxy", minimum)
	}
	seen := make(map[int]struct{}, len(networks))
	seenAddresses := make(map[string]struct{}, len(networks))
	for _, network := range networks {
		address, err := netip.ParseAddr(network.Address)
		if network.InterfaceIndex <= 0 || network.Name == "" || err != nil || !isUsableDownloadAddress(address) {
			return fmt.Errorf("adapter %q has an invalid interface index or IPv4 address", network.Name)
		}
		if _, exists := seen[network.InterfaceIndex]; exists {
			return fmt.Errorf("network interface %d is selected more than once", network.InterfaceIndex)
		}
		seen[network.InterfaceIndex] = struct{}{}
		if _, exists := seenAddresses[address.String()]; exists {
			return fmt.Errorf("selected adapters share the same IPv4 address %s", address)
		}
		seenAddresses[address.String()] = struct{}{}
	}
	return nil
}

func (p *LocalProxyManager) setNetworksLocked(networks []DownloadNetwork) {
	selected := make([]*localProxyAdapter, 0, len(networks))
	selectedIndexes := make(map[int]struct{}, len(networks))
	for _, network := range networks {
		adapter := p.byIndex[network.InterfaceIndex]
		if adapter == nil || adapter.network.Address != network.Address {
			adapter = &localProxyAdapter{network: network, lastAt: time.Now()}
			p.byIndex[network.InterfaceIndex] = adapter
		}
		selected = append(selected, adapter)
		selectedIndexes[network.InterfaceIndex] = struct{}{}
	}
	p.adapters = selected
	for index, adapter := range p.byIndex {
		if _, ok := selectedIndexes[index]; !ok && adapter.activeConnections.Load() == 0 {
			delete(p.byIndex, index)
		}
	}
}

func (p *LocalProxyManager) Stop() {
	p.opMu.Lock()
	defer p.opMu.Unlock()

	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.running = false
	socksListener, httpListener := p.socksListener, p.httpListener
	p.socksListener, p.httpListener = nil, nil
	connections := make([]net.Conn, 0, len(p.activeConns))
	for conn := range p.activeConns {
		connections = append(connections, conn)
	}
	p.mu.Unlock()

	_ = socksListener.Close()
	_ = httpListener.Close()
	p.acceptWG.Wait()
	for _, conn := range connections {
		_ = conn.Close()
	}
	p.connWG.Wait()

	p.mu.Lock()
	p.adapters = nil
	p.socksAddress = ""
	p.httpAddress = ""
	p.mu.Unlock()
}

func (p *LocalProxyManager) Snapshot() LocalProxySnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	snapshot := LocalProxySnapshot{
		Running:      p.running,
		SOCKSAddress: p.socksAddress,
		HTTPAddress:  p.httpAddress,
		Adapters:     make([]LocalProxyAdapterSnapshot, 0, len(p.adapters)),
	}
	for _, adapter := range p.adapters {
		now := time.Now()
		adapter.sampleMu.Lock()
		bytesUp, bytesDown := adapter.bytesUp.Load(), adapter.bytesDown.Load()
		if elapsed := now.Sub(adapter.lastAt).Seconds(); elapsed > 0 {
			adapter.upRate = int64(float64(bytesUp-adapter.lastUp) / elapsed)
			adapter.downRate = int64(float64(bytesDown-adapter.lastDown) / elapsed)
			adapter.lastUp, adapter.lastDown, adapter.lastAt = bytesUp, bytesDown, now
		}
		adapter.sampleMu.Unlock()
		item := LocalProxyAdapterSnapshot{
			InterfaceIndex:    adapter.network.InterfaceIndex,
			Name:              adapter.network.Name,
			Address:           adapter.network.Address,
			ActiveConnections: adapter.activeConnections.Load(),
			BytesUp:           bytesUp,
			BytesDown:         bytesDown,
			UpRateBps:         adapter.upRate,
			DownRateBps:       adapter.downRate,
			Failures:          adapter.failures.Load(),
			Healthy:           time.Now().UnixNano() >= adapter.unhealthyUntil.Load(),
		}
		if lastError := adapter.lastError.Load(); lastError != nil {
			item.LastError = *lastError
		}
		snapshot.Adapters = append(snapshot.Adapters, item)
	}
	return snapshot
}

func (p *LocalProxyManager) acceptLoop(listener net.Listener, socks bool) {
	defer p.acceptWG.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		if !p.running {
			p.mu.Unlock()
			_ = conn.Close()
			return
		}
		p.activeConns[conn] = struct{}{}
		p.connWG.Add(1)
		p.mu.Unlock()

		go func() {
			defer p.connWG.Done()
			defer func() {
				p.mu.Lock()
				delete(p.activeConns, conn)
				p.mu.Unlock()
				_ = conn.Close()
			}()
			if socks {
				p.handleSOCKS(conn)
			} else {
				p.handleHTTP(conn)
			}
		}()
	}
}

func (p *LocalProxyManager) adapterCandidates() []*localProxyAdapter {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.adapters) == 0 {
		return nil
	}
	start := int(p.nextAdapter.Add(1)-1) % len(p.adapters)
	ordered := make([]*localProxyAdapter, 0, len(p.adapters))
	for i := 0; i < len(p.adapters); i++ {
		ordered = append(ordered, p.adapters[(start+i)%len(p.adapters)])
	}
	healthy := make([]*localProxyAdapter, 0, len(ordered))
	unhealthy := make([]*localProxyAdapter, 0, len(ordered))
	for _, adapter := range ordered {
		if time.Now().UnixNano() >= adapter.unhealthyUntil.Load() {
			healthy = append(healthy, adapter)
		} else {
			unhealthy = append(unhealthy, adapter)
		}
	}
	return append(healthy, unhealthy...)
}

func (p *LocalProxyManager) dialRemote(destination string) (*meteredProxyConn, error) {
	candidates := p.adapterCandidates()
	if len(candidates) == 0 {
		return nil, errors.New("local proxy has no selected adapters")
	}
	var failures []string
	for _, adapter := range candidates {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		conn, err := p.dial(ctx, adapter.network, destination)
		cancel()
		if err != nil {
			adapter.markFailure(err)
			failures = append(failures, fmt.Sprintf("%s: %v", adapter.network.Name, err))
			continue
		}
		adapter.unhealthyUntil.Store(0)
		adapter.lastError.Store(nil)
		adapter.activeConnections.Add(1)
		return &meteredProxyConn{Conn: conn, adapter: adapter}, nil
	}
	return nil, fmt.Errorf("all adapter connections failed: %s", strings.Join(failures, "; "))
}

func (a *localProxyAdapter) markFailure(err error) {
	a.failures.Add(1)
	a.unhealthyUntil.Store(time.Now().Add(proxyFailureHold).UnixNano())
	message := err.Error()
	a.lastError.Store(&message)
}

func dialProxyAdapter(ctx context.Context, network DownloadNetwork, destination string) (net.Conn, error) {
	localAddress, err := netip.ParseAddr(network.Address)
	if err != nil || !localAddress.Is4() {
		return nil, fmt.Errorf("invalid local IPv4 address %q", network.Address)
	}
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		LocalAddr: &net.TCPAddr{IP: net.IP(localAddress.AsSlice())},
		Control: func(_, _ string, raw syscall.RawConn) error {
			return bindTCPInterface(raw, network.InterfaceIndex)
		},
	}
	return dialer.DialContext(ctx, "tcp4", destination)
}

type meteredProxyConn struct {
	net.Conn
	adapter   *localProxyAdapter
	closeOnce sync.Once
	closeErr  error
}

func (c *meteredProxyConn) Read(buffer []byte) (int, error) {
	n, err := c.Conn.Read(buffer)
	if n > 0 {
		c.adapter.bytesDown.Add(int64(n))
	}
	c.recordError(err)
	return n, err
}

func (c *meteredProxyConn) Write(buffer []byte) (int, error) {
	n, err := c.Conn.Write(buffer)
	if n > 0 {
		c.adapter.bytesUp.Add(int64(n))
	}
	c.recordError(err)
	return n, err
}

func (c *meteredProxyConn) Close() error {
	c.closeOnce.Do(func() {
		c.adapter.activeConnections.Add(-1)
		c.closeErr = c.Conn.Close()
	})
	return c.closeErr
}

func (c *meteredProxyConn) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return c.Conn.Close()
}

func (c *meteredProxyConn) recordError(err error) {
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		c.adapter.markFailure(err)
	}
}

func (p *LocalProxyManager) handleSOCKS(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(conn)
	var header [2]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil || header[0] != 5 || header[1] == 0 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return
	}
	noAuth := false
	for _, method := range methods {
		if method == 0 {
			noAuth = true
			break
		}
	}
	if !noAuth {
		_, _ = conn.Write([]byte{5, 0xff})
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}

	var request [4]byte
	if _, err := io.ReadFull(reader, request[:]); err != nil || request[0] != 5 {
		return
	}
	if request[1] != 1 {
		writeSOCKSReply(conn, 7)
		return
	}
	host, err := readSOCKSHost(reader, request[3])
	if err != nil {
		writeSOCKSReply(conn, 8)
		return
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(reader, portBytes[:]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portBytes[:])
	destination, err := resolveProxyDestination(host, port)
	if err != nil {
		writeSOCKSReply(conn, 4)
		return
	}
	upstream, err := p.dialRemote(destination)
	if err != nil {
		writeSOCKSReply(conn, 5)
		return
	}
	defer upstream.Close()
	if writeSOCKSReply(conn, 0) != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	bridgeProxyConnections(conn, reader, upstream)
}

func readSOCKSHost(reader io.Reader, addressType byte) (string, error) {
	switch addressType {
	case 1:
		var address [4]byte
		if _, err := io.ReadFull(reader, address[:]); err != nil {
			return "", err
		}
		return net.IP(address[:]).String(), nil
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return "", err
		}
		if length[0] == 0 {
			return "", errors.New("empty SOCKS hostname")
		}
		name := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, name); err != nil {
			return "", err
		}
		return string(name), nil
	case 4:
		var address [16]byte
		if _, err := io.ReadFull(reader, address[:]); err != nil {
			return "", err
		}
		return net.IP(address[:]).String(), nil
	default:
		return "", errors.New("unsupported SOCKS address type")
	}
}

func resolveProxyDestination(host string, port uint16) (string, error) {
	if port == 0 || host == "" {
		return "", errors.New("invalid proxy destination")
	}
	if address, err := netip.ParseAddr(host); err == nil {
		if !address.Is4() {
			return "", errors.New("only IPv4 adapter paths are available")
		}
		return net.JoinHostPort(address.String(), strconv.Itoa(int(port))), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil || len(addresses) == 0 {
		if err == nil {
			err = errors.New("hostname has no IPv4 address")
		}
		return "", err
	}
	return net.JoinHostPort(addresses[0].String(), strconv.Itoa(int(port))), nil
}

func writeSOCKSReply(conn net.Conn, status byte) error {
	_, err := conn.Write([]byte{5, status, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}

func (p *LocalProxyManager) handleHTTP(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(conn)
	request, err := http.ReadRequest(reader)
	if err != nil {
		writeProxyHTTPError(conn, http.StatusBadRequest, "invalid proxy request")
		return
	}
	if request.Method == http.MethodConnect {
		p.handleHTTPConnect(conn, reader, request)
		return
	}
	if request.URL == nil || !request.URL.IsAbs() || request.URL.Scheme != "http" || request.URL.Host == "" {
		writeProxyHTTPError(conn, http.StatusBadRequest, "use an absolute http URL or CONNECT")
		return
	}
	host, port, err := net.SplitHostPort(request.URL.Host)
	if err != nil {
		host = request.URL.Hostname()
		if host == "" {
			host = request.URL.Host
		}
		port = "80"
	}
	if request.URL.Port() != "" {
		port = request.URL.Port()
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		writeProxyHTTPError(conn, http.StatusBadRequest, "invalid proxy destination port")
		return
	}
	destination, err := resolveProxyDestination(host, uint16(portNumber))
	if err != nil {
		writeProxyHTTPError(conn, http.StatusBadGateway, err.Error())
		return
	}
	upstream, err := p.dialRemote(destination)
	if err != nil {
		writeProxyHTTPError(conn, http.StatusBadGateway, err.Error())
		return
	}
	defer upstream.Close()

	request.Close = true
	request.Header.Del("Proxy-Connection")
	request.Header.Del("Proxy-Authorization")
	request.Header.Set("Connection", "close")
	if err := request.Write(upstream); err != nil {
		return
	}
	response, err := http.ReadResponse(bufio.NewReader(upstream), request)
	if err != nil {
		writeProxyHTTPError(conn, http.StatusBadGateway, "upstream returned an invalid response")
		return
	}
	response.Close = true
	response.Header.Set("Connection", "close")
	_ = response.Write(conn)
	_ = response.Body.Close()
}

func (p *LocalProxyManager) handleHTTPConnect(conn net.Conn, reader *bufio.Reader, request *http.Request) {
	host, port, err := net.SplitHostPort(request.Host)
	if err != nil {
		host = request.URL.Hostname()
		port = request.URL.Port()
	}
	if port == "" {
		port = "443"
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		writeProxyHTTPError(conn, http.StatusBadRequest, "invalid CONNECT port")
		return
	}
	destination, err := resolveProxyDestination(host, uint16(portNumber))
	if err != nil {
		writeProxyHTTPError(conn, http.StatusBadGateway, err.Error())
		return
	}
	upstream, err := p.dialRemote(destination)
	if err != nil {
		writeProxyHTTPError(conn, http.StatusBadGateway, err.Error())
		return
	}
	defer upstream.Close()
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	bridgeProxyConnections(conn, reader, upstream)
}

func writeProxyHTTPError(conn net.Conn, status int, message string) {
	body := message + "\n"
	_, _ = fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, http.StatusText(status), len(body), body)
}

func bridgeProxyConnections(client net.Conn, clientReader io.Reader, upstream *meteredProxyConn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, clientReader)
		_ = upstream.CloseWrite()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		if conn, ok := client.(interface{ CloseWrite() error }); ok {
			_ = conn.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	_ = client.Close()
	_ = upstream.Close()
	<-done
}
