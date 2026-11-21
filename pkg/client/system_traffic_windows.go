//go:build windows

package client

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const systemTrafficCountersSupported = true

var getInterfaceEntry2 = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetIfEntry2")

func readSystemInterfaceByteCounters(networks []DownloadNetwork) (map[int]interfaceByteCounters, error) {
	if err := getInterfaceEntry2.Find(); err != nil {
		return nil, fmt.Errorf("load Windows interface counter API: %w", err)
	}
	counters := make(map[int]interfaceByteCounters, len(networks))
	for _, network := range networks {
		row := windows.MibIfRow2{InterfaceIndex: uint32(network.InterfaceIndex)}
		result, _, _ := getInterfaceEntry2.Call(uintptr(unsafe.Pointer(&row)))
		if result != 0 {
			return nil, fmt.Errorf("read Windows counters for interface %d: %w", network.InterfaceIndex, windows.Errno(result))
		}
		counters[network.InterfaceIndex] = interfaceByteCounters{
			received: row.InOctets,
			sent:     row.OutOctets,
		}
	}
	return counters, nil
}
