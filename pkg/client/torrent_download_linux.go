//go:build linux

package client

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const torrentNetworkBindingSupported = true

func dialTorrentTCP(ctx context.Context, network DownloadNetwork, address string) (net.Conn, error) {
	ip, err := netip.ParseAddr(network.Address)
	if err != nil || !ip.Is4() {
		return nil, fmt.Errorf("invalid IPv4 address for %s", network.Name)
	}
	dialer := &net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
		LocalAddr: &net.TCPAddr{IP: net.IP(ip.AsSlice())},
		Control: func(_, _ string, raw syscall.RawConn) error {
			return bindTCPInterface(raw, network.InterfaceIndex)
		},
	}
	return dialer.DialContext(ctx, "tcp4", address)
}

func listenTorrentPacket(network, address string, adapter DownloadNetwork) (net.PacketConn, error) {
	ip, err := netip.ParseAddr(adapter.Address)
	if err != nil || !ip.Is4() {
		return nil, fmt.Errorf("invalid IPv4 address for %s", adapter.Name)
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		port = "0"
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return nil, fmt.Errorf("invalid torrent UDP port %q", port)
	}
	localAddress := net.JoinHostPort(strings.TrimSpace(adapter.Address), port)
	packet, err := net.ListenPacket(network, localAddress)
	if err != nil {
		return nil, fmt.Errorf("listen for torrent traffic on %s: %w", adapter.Name, err)
	}
	udp, ok := packet.(*net.UDPConn)
	if !ok {
		_ = packet.Close()
		return nil, fmt.Errorf("torrent listener on %s is not UDP", adapter.Name)
	}
	if err := bindUDPInterface(udp, adapter.InterfaceIndex); err != nil {
		_ = packet.Close()
		return nil, fmt.Errorf("bind torrent UDP socket to %s: %w", adapter.Name, err)
	}
	return packet, nil
}
