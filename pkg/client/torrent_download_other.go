//go:build !windows && !linux

package client

import (
	"context"
	"errors"
	"net"
)

const torrentNetworkBindingSupported = false

var errTorrentNetworkBindingUnsupported = errors.New("adapter-bound torrent downloads are not supported on this platform")

func dialTorrentTCP(context.Context, DownloadNetwork, string) (net.Conn, error) {
	return nil, errTorrentNetworkBindingUnsupported
}

func listenTorrentPacket(string, string, DownloadNetwork) (net.PacketConn, error) {
	return nil, errTorrentNetworkBindingUnsupported
}
