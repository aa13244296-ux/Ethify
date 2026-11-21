//go:build windows

package client

import (
	"errors"
	"fmt"
	"net/netip"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var deleteIPForwardEntry2 = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("DeleteIpForwardEntry2")

func cleanupStaleLocalRouterRoutes() error {
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_INET, &table); err != nil {
		return fmt.Errorf("read IPv4 routes: %w", err)
	}
	if table == nil {
		return nil
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))

	for _, route := range table.Rows() {
		prefix, ok := localRouterRoutePrefix(route)
		if !ok || !isLocalRouterSplitDefaultRoute(prefix) || route.Immortal != 0 {
			continue
		}
		alias, err := windowsInterfaceAlias(route.InterfaceIndex)
		if err != nil {
			return fmt.Errorf("identify interface %d for route %s: %w", route.InterfaceIndex, prefix, err)
		}
		if !shouldDeleteLocalRouterRoute(prefix, alias) {
			continue
		}
		if err := deleteIPv4Route(route); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
			return fmt.Errorf("delete stale local-router route %s on interface %d: %w", prefix, route.InterfaceIndex, err)
		}
	}
	return nil
}

func localRouterRoutePrefix(route windows.MibIpForwardRow2) (string, bool) {
	if route.DestinationPrefix.Prefix.Family != windows.AF_INET || route.DestinationPrefix.PrefixLength > 32 {
		return "", false
	}
	address := (*windows.RawSockaddrInet4)(unsafe.Pointer(&route.DestinationPrefix.Prefix))
	prefix := netip.PrefixFrom(netip.AddrFrom4(address.Addr), int(route.DestinationPrefix.PrefixLength)).Masked()
	return prefix.String(), true
}

func windowsInterfaceAlias(ifIndex uint32) (string, error) {
	if err := getInterfaceEntry2.Find(); err != nil {
		return "", fmt.Errorf("load GetIfEntry2: %w", err)
	}
	row := windows.MibIfRow2{InterfaceIndex: ifIndex}
	result, _, _ := getInterfaceEntry2.Call(uintptr(unsafe.Pointer(&row)))
	if result != 0 {
		return "", windows.Errno(result)
	}
	return windows.UTF16ToString(row.Alias[:]), nil
}

func deleteIPv4Route(route windows.MibIpForwardRow2) error {
	if err := deleteIPForwardEntry2.Find(); err != nil {
		return fmt.Errorf("load DeleteIpForwardEntry2: %w", err)
	}
	result, _, _ := deleteIPForwardEntry2.Call(uintptr(unsafe.Pointer(&route)))
	if result != 0 {
		return syscall.Errno(result)
	}
	return nil
}
