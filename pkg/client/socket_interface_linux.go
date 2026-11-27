//go:build linux

package client

import (
	"fmt"
	"net"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	unixCapNetAdmin = 12
	unixCapNetRaw   = 13
)

var activeLinuxRouterMark atomic.Int32

func bindUDPInterface(conn *net.UDPConn, interfaceIndex int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	return bindUDPInterfaceRaw(raw, interfaceIndex)
}

func bindUDPInterfaceRaw(raw syscall.RawConn, interfaceIndex int) error {
	interfaceName, err := linuxInterfaceName(interfaceIndex)
	if err != nil {
		return err
	}
	var bindErr error
	controlErr := raw.Control(func(fd uintptr) {
		bindErr = bindLinuxSocket(fd, interfaceName)
	})
	if controlErr != nil {
		return controlErr
	}
	return bindErr
}

func bindTCPInterface(raw syscall.RawConn, interfaceIndex int) error {
	interfaceName, err := linuxInterfaceName(interfaceIndex)
	if err != nil {
		return err
	}
	var bindErr error
	controlErr := raw.Control(func(fd uintptr) {
		bindErr = bindLinuxSocket(fd, interfaceName)
	})
	if controlErr != nil {
		return controlErr
	}
	return bindErr
}

func bindLinuxSocket(fd uintptr, interfaceName string) error {
	if err := unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, interfaceName); err != nil {
		if err == unix.EPERM || err == unix.EACCES {
			return fmt.Errorf("bind socket to %s: permission denied (run Ethify as root or grant CAP_NET_RAW)", interfaceName)
		}
		return fmt.Errorf("bind socket to %s: %w", interfaceName, err)
	}
	if mark := activeLinuxRouterMark.Load(); mark != 0 {
		if err := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, int(mark)); err != nil {
			return fmt.Errorf("mark Ethify socket for policy-routing bypass: %w (root or CAP_NET_ADMIN is required)", err)
		}
	}
	return nil
}

func linuxInterfaceName(interfaceIndex int) (string, error) {
	if interfaceIndex <= 0 {
		return "", fmt.Errorf("invalid Linux interface index %d", interfaceIndex)
	}
	iface, err := net.InterfaceByIndex(interfaceIndex)
	if err != nil {
		return "", fmt.Errorf("resolve Linux interface %d: %w", interfaceIndex, err)
	}
	if iface.Name == "" {
		return "", fmt.Errorf("Linux interface %d has no name", interfaceIndex)
	}
	return iface.Name, nil
}
