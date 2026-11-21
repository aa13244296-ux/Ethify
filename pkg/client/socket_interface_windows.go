//go:build windows

package client

import (
	"fmt"
	"math/bits"
	"net"
	"syscall"

	"golang.org/x/sys/windows"
)

const windowsIPUnicastIF = 31

func windowsUnicastInterfaceValue(interfaceIndex uint32) int {
	return int(int32(bits.ReverseBytes32(interfaceIndex)))
}

func bindUDPInterface(conn *net.UDPConn, interfaceIndex int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("get UDP socket handle: %w", err)
	}
	return bindSocketInterface(raw, interfaceIndex)
}

func bindTCPInterface(raw syscall.RawConn, interfaceIndex int) error {
	return bindSocketInterface(raw, interfaceIndex)
}

func bindSocketInterface(raw syscall.RawConn, interfaceIndex int) error {
	if interfaceIndex <= 0 {
		return fmt.Errorf("invalid interface index %d", interfaceIndex)
	}
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		value := windowsUnicastInterfaceValue(uint32(interfaceIndex))
		socketErr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, windowsIPUnicastIF, value)
	}); err != nil {
		return fmt.Errorf("set outgoing interface: %w", err)
	}
	if socketErr != nil {
		return fmt.Errorf("set outgoing interface: %w", socketErr)
	}
	return nil
}
