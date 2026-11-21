//go:build windows

package client

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func eligibleDownloadInterfaceIndices() (map[int]struct{}, error) {
	const initialBufferSize = 16 * 1024
	size := uint32(initialBufferSize)
	buffer := make([]byte, size)
	adapters := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
	err := windows.GetAdaptersAddresses(uint32(windows.AF_INET), 0, 0, adapters, &size)
	if err == windows.ERROR_BUFFER_OVERFLOW {
		buffer = make([]byte, size)
		adapters = (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		err = windows.GetAdaptersAddresses(uint32(windows.AF_INET), 0, 0, adapters, &size)
	}
	if err != nil {
		return nil, fmt.Errorf("list Windows download interfaces: %w", err)
	}

	eligible := make(map[int]struct{})
	for adapter := adapters; adapter != nil; adapter = adapter.Next {
		if adapter.IfIndex == 0 || adapter.OperStatus != windows.IfOperStatusUp ||
			!isPhysicalDownloadInterfaceType(adapter.IfType) {
			continue
		}
		eligible[int(adapter.IfIndex)] = struct{}{}
	}
	return eligible, nil
}

func isPhysicalDownloadInterfaceType(ifType uint32) bool {
	return ifType == windows.IF_TYPE_ETHERNET_CSMACD || ifType == windows.IF_TYPE_IEEE80211
}
