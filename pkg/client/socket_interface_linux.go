//go:build linux

package client

import (
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

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
		bindErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, interfaceName)
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
		bindErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, interfaceName)
	})
	if controlErr != nil {
		return controlErr
	}
	return bindErr
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
