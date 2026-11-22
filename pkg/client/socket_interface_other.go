//go:build !windows && !linux

package client

import (
	"net"
	"syscall"
)

func bindUDPInterface(_ *net.UDPConn, _ int) error {
	return nil
}

func bindTCPInterface(_ syscall.RawConn, _ int) error {
	return nil
}
