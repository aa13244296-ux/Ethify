package client

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestLocalProxyBalancesHTTPConnectionsAcrossAdapters(t *testing.T) {
	var sources sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			sources.Store(host, struct{}{})
		}
		_, _ = io.WriteString(w, host)
	}))
	defer server.Close()

	proxy := NewLocalProxyManager()
	proxy.dial = dialProxyForTest
	if err := proxy.startNetworks(testProxyNetworks(t), "127.0.0.1:0", "127.0.0.1:0"); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer proxy.Stop()
	status := proxy.Snapshot()

	proxyURL, err := url.Parse("http://" + status.HTTPAddress)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy:             http.ProxyURL(proxyURL),
		DisableKeepAlives: true,
	}}
	defer client.CloseIdleConnections()

	for i := 0; i < 2; i++ {
		response, err := client.Get(server.URL)
		if err != nil {
			t.Fatalf("proxy request %d: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("proxy request %d status = %d", i, response.StatusCode)
		}
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "secure")
	}))
	defer tlsServer.Close()
	tlsClient := tlsServer.Client()
	tlsTransport := tlsClient.Transport.(*http.Transport).Clone()
	tlsTransport.Proxy = http.ProxyURL(proxyURL)
	tlsTransport.DisableKeepAlives = true
	tlsClient.Transport = tlsTransport
	tlsResponse, err := tlsClient.Get(tlsServer.URL)
	if err != nil {
		t.Fatalf("HTTP CONNECT request: %v", err)
	}
	_, _ = io.Copy(io.Discard, tlsResponse.Body)
	_ = tlsResponse.Body.Close()
	if tlsResponse.StatusCode != http.StatusOK {
		t.Fatalf("HTTP CONNECT status = %d, want %d", tlsResponse.StatusCode, http.StatusOK)
	}
	if _, ok := sources.Load("127.0.0.1"); !ok {
		t.Fatal("first adapter source address was not used")
	}
	if _, ok := sources.Load("127.0.0.2"); !ok {
		t.Fatal("second adapter source address was not used")
	}

	snapshot := proxy.Snapshot()
	if !snapshot.Running || len(snapshot.Adapters) != 2 {
		t.Fatalf("unexpected proxy snapshot: %+v", snapshot)
	}
	if snapshot.Adapters[0].BytesUp == 0 || snapshot.Adapters[1].BytesUp == 0 {
		t.Fatalf("proxy traffic was not counted on both adapters: %+v", snapshot.Adapters)
	}
}

func TestLocalProxySOCKS5Connect(t *testing.T) {
	target, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := target.Close(); err != nil {
			t.Errorf("close target server: %v", err)
		}
	})
	go func() {
		for {
			conn, err := target.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() {
					if err := conn.Close(); err != nil {
						t.Errorf("close accepted target connection: %v", err)
					}
				}()
				buffer := make([]byte, 2)
				if _, err := io.ReadFull(conn, buffer); err == nil {
					_, _ = conn.Write([]byte("ok"))
				}
			}()
		}
	}()

	proxy := NewLocalProxyManager()
	proxy.dial = dialProxyForTest
	if err := proxy.startNetworks(testProxyNetworks(t), "127.0.0.1:0", "127.0.0.1:0"); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer proxy.Stop()

	conn, err := net.DialTimeout("tcp", proxy.Snapshot().SOCKSAddress, time.Second)
	if err != nil {
		t.Fatalf("connect SOCKS5 proxy: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close SOCKS client connection: %v", err)
		}
	})
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	var method [2]byte
	if _, err := io.ReadFull(conn, method[:]); err != nil || method != [2]byte{5, 0} {
		t.Fatalf("SOCKS method response = %v, error %v", method, err)
	}

	targetAddress, err := netip.ParseAddrPort(target.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ip := targetAddress.Addr().As4()
	port := targetAddress.Port()
	request := []byte{5, 1, 0, 1, ip[0], ip[1], ip[2], ip[3], byte(port >> 8), byte(port)}
	if _, err := conn.Write(request); err != nil {
		t.Fatal(err)
	}
	var reply [10]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0 {
		t.Fatalf("SOCKS CONNECT status = %d, want success", reply[1])
	}
	if _, err := conn.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 2)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "ok" {
		t.Fatalf("SOCKS tunnel response = %q, want %q", got, "ok")
	}
}

func TestLocalProxyRetriesNextAdapterWhenDialFails(t *testing.T) {
	first := &localProxyAdapter{network: DownloadNetwork{
		InterfaceIndex: 2, Name: "first", Address: "192.0.2.1",
	}}
	second := &localProxyAdapter{network: DownloadNetwork{
		InterfaceIndex: 3, Name: "second", Address: "192.0.2.2",
	}}
	proxy := NewLocalProxyManager()
	proxy.adapters = []*localProxyAdapter{first, second}
	proxy.dial = func(_ context.Context, network DownloadNetwork, _ string) (net.Conn, error) {
		if network.InterfaceIndex == first.network.InterfaceIndex {
			return nil, errors.New("connection reset")
		}
		client, server := net.Pipe()
		go func() {
			if err := server.Close(); err != nil {
				t.Errorf("close pipe server: %v", err)
			}
		}()
		return client, nil
	}

	conn, err := proxy.dialRemote("198.51.100.1:443")
	if err != nil {
		t.Fatalf("dial with failover: %v", err)
	}
	_ = conn.Close()
	if first.failures.Load() != 1 || first.unhealthyUntil.Load() <= time.Now().UnixNano() {
		t.Fatalf("failed adapter was not placed in cooldown: failures=%d", first.failures.Load())
	}
	if first.activeConnections.Load() != 0 || second.activeConnections.Load() != 0 {
		t.Fatalf("connection counts did not return to zero: first=%d second=%d",
			first.activeConnections.Load(), second.activeConnections.Load())
	}
}

func TestRunningLocalProxyCanDropToOneAvailableAdapter(t *testing.T) {
	networks := testProxyNetworks(t)
	proxy := NewLocalProxyManager()
	if err := proxy.startNetworks(networks, "127.0.0.1:0", "127.0.0.1:0"); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer proxy.Stop()

	if err := proxy.startNetworks(networks[:1], "127.0.0.1:0", "127.0.0.1:0"); err != nil {
		t.Fatalf("update running proxy to one adapter: %v", err)
	}
	snapshot := proxy.Snapshot()
	if !snapshot.Running || len(snapshot.Adapters) != 1 {
		t.Fatalf("proxy did not drop the unavailable adapter: %+v", snapshot)
	}
}

func TestValidateProxyNetworksRequiresDistinctActiveAdapters(t *testing.T) {
	if err := validateProxyNetworks(testProxyNetworks(t)); err != nil {
		t.Fatalf("valid adapters rejected: %v", err)
	}
	if err := validateProxyNetworks(testProxyNetworks(t)[:1]); err == nil {
		t.Fatal("single adapter should not enable a multi-adapter proxy")
	}
	duplicate := testProxyNetworks(t)
	duplicate[1].InterfaceIndex = duplicate[0].InterfaceIndex
	if err := validateProxyNetworks(duplicate); err == nil {
		t.Fatal("duplicate interface should be rejected")
	}
	duplicate = testProxyNetworks(t)
	duplicate[1].Address = duplicate[0].Address
	if err := validateProxyNetworks(duplicate); err == nil {
		t.Fatal("duplicate source address should be rejected")
	}
}

func testProxyNetworks(t *testing.T) []DownloadNetwork {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatalf("list interfaces: %v", err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return []DownloadNetwork{
				{InterfaceIndex: iface.Index, Name: "test adapter one", Address: "192.0.2.1"},
				{InterfaceIndex: iface.Index + 1, Name: "test adapter two", Address: "192.0.2.2"},
			}
		}
	}
	t.Skip("loopback interface is unavailable")
	return nil
}

func dialProxyForTest(ctx context.Context, network DownloadNetwork, destination string) (net.Conn, error) {
	localAddress := net.ParseIP("127.0.0.1")
	if network.Address == "192.0.2.2" {
		localAddress = net.ParseIP("127.0.0.2")
	}
	dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: localAddress}}
	return dialer.DialContext(ctx, "tcp4", destination)
}
