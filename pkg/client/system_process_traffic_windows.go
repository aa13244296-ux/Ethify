//go:build windows

package client

import (
	"fmt"
	"log"
	"net/netip"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	tcpTableOwnerPIDAll = 5
	afInet              = 2
	tcpEstablished      = 5
	tcpEstatsData       = 1
)

type tcpRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	ProcessID  uint32
}

type tcpTableOwnerPIDData struct {
	Count uint32
	Rows  [1]tcpRowOwnerPID
}

type tcpRow struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
}

type tcpEstatsDataRODv0 struct {
	DataBytesOut      uint64
	DataSegsOut       uint64
	DataBytesIn       uint64
	DataSegsIn        uint64
	SegsOut           uint64
	SegsIn            uint64
	SoftErrors        uint32
	SoftErrorReason   uint32
	SndUna            uint32
	SndNxt            uint32
	SndMax            uint32
	ThruBytesAcked    uint64
	RcvNxt            uint32
	ThruBytesReceived uint64
}

var (
	getExtendedTCPTable    = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")
	getTCPConnectionEStats = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetPerTcpConnectionEStats")
	setTCPConnectionEStats = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("SetPerTcpConnectionEStats")
	queryProcessImageName  = windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")
)

func readTCPProcessCounters(networks []DownloadNetwork, enabled map[string]struct{}) ([]processFlowCounter, error) {
	table, err := readTCPTable()
	if err != nil {
		return nil, err
	}
	if err := setTCPConnectionEStats.Find(); err != nil {
		return nil, fmt.Errorf("load Windows TCP statistics API: %w", err)
	}
	if err := getTCPConnectionEStats.Find(); err != nil {
		return nil, fmt.Errorf("load Windows TCP statistics API: %w", err)
	}
	interfaceByAddress := make(map[uint32]int, len(networks))
	for _, network := range networks {
		address, err := parseIPv4Address(network.Address)
		if err == nil {
			interfaceByAddress[address] = network.InterfaceIndex
		}
	}

	var counters []processFlowCounter
	activeKeys := make(map[string]struct{}, len(table))
	names := make(map[uint32]string)
	var firstStatsError uintptr
	for _, connection := range table {
		if connection.State != tcpEstablished || connection.ProcessID == 0 {
			continue
		}
		interfaceIndex, ok := interfaceByAddress[connection.LocalAddr]
		if !ok {
			continue
		}

		row := tcpRow{
			State:      connection.State,
			LocalAddr:  connection.LocalAddr,
			LocalPort:  connection.LocalPort,
			RemoteAddr: connection.RemoteAddr,
			RemotePort: connection.RemotePort,
		}
		key := fmt.Sprintf("%d:%08x:%08x:%08x:%08x", connection.ProcessID, connection.LocalAddr, connection.LocalPort, connection.RemoteAddr, connection.RemotePort)
		activeKeys[key] = struct{}{}
		if _, ok := enabled[key]; !ok {
			enabledFlag := byte(1)
			result, _, _ := setTCPConnectionEStats.Call(
				uintptr(unsafe.Pointer(&row)),
				tcpEstatsData,
				uintptr(unsafe.Pointer(&enabledFlag)),
				0,
				1,
			)
			runtime.KeepAlive(&row)
			runtime.KeepAlive(&enabledFlag)
			if result != 0 {
				if firstStatsError == 0 {
					firstStatsError = result
				}
				continue
			}
			enabled[key] = struct{}{}
		}

		var stats tcpEstatsDataRODv0
		result, _, _ := getTCPConnectionEStats.Call(
			uintptr(unsafe.Pointer(&row)),
			tcpEstatsData,
			0, 0, 0,
			0, 0, 0,
			uintptr(unsafe.Pointer(&stats)), 0, unsafe.Sizeof(stats),
		)
		runtime.KeepAlive(&row)
		runtime.KeepAlive(&stats)
		if result != 0 {
			if firstStatsError == 0 {
				firstStatsError = result
			}
			continue
		}
		name, ok := names[connection.ProcessID]
		if !ok {
			name = processName(connection.ProcessID)
			names[connection.ProcessID] = name
		}
		counters = append(counters, processFlowCounter{
			Key:            key,
			ProcessID:      connection.ProcessID,
			ProcessName:    name,
			InterfaceIndex: interfaceIndex,
			Received:       stats.DataBytesIn,
			Sent:           stats.DataBytesOut,
		})
	}
	if len(activeKeys) > 0 && len(counters) == 0 && firstStatsError != 0 {
		return nil, fmt.Errorf("windows cannot read per-process TCP statistics: %w", windows.Errno(firstStatsError))
	}
	for key := range enabled {
		if _, ok := activeKeys[key]; !ok {
			delete(enabled, key)
		}
	}
	return counters, nil
}

func readTCPTable() ([]tcpRowOwnerPID, error) {
	if err := getExtendedTCPTable.Find(); err != nil {
		return nil, fmt.Errorf("load Windows TCP table API: %w", err)
	}
	var size uint32
	result, _, _ := getExtendedTCPTable.Call(
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
		afInet,
		tcpTableOwnerPIDAll,
		0,
	)
	if result != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
		return nil, fmt.Errorf("size Windows TCP table: %w", windows.Errno(result))
	}
	if size < uint32(unsafe.Sizeof(uint32(0))) {
		return nil, fmt.Errorf("windows TCP table returned invalid size %d", size)
	}
	for attempt := 0; attempt < 3; attempt++ {
		buffer := make([]byte, size)
		result, _, _ = getExtendedTCPTable.Call(
			uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(unsafe.Pointer(&size)),
			0,
			afInet,
			tcpTableOwnerPIDAll,
			0,
		)
		runtime.KeepAlive(buffer)
		if result == uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
			continue
		}
		if result != 0 {
			return nil, fmt.Errorf("read Windows TCP table: %w", windows.Errno(result))
		}
		table := (*tcpTableOwnerPIDData)(unsafe.Pointer(&buffer[0]))
		if table.Count == 0 {
			return nil, nil
		}
		rowsSize := uintptr(table.Count) * unsafe.Sizeof(tcpRowOwnerPID{})
		if uint64(rowsSize)+uint64(unsafe.Sizeof(uint32(0))) > uint64(size) {
			return nil, fmt.Errorf("windows TCP table has invalid row count %d", table.Count)
		}
		rows := unsafe.Slice(&table.Rows[0], int(table.Count))
		resultRows := append([]tcpRowOwnerPID(nil), rows...)
		runtime.KeepAlive(buffer)
		return resultRows, nil
	}
	return nil, fmt.Errorf("windows TCP table changed during three read attempts")
}

func parseIPv4Address(value string) (uint32, error) {
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is4() {
		return 0, fmt.Errorf("invalid IPv4 address %q", value)
	}
	octets := address.As4()
	parsed := uint32(octets[0]) | uint32(octets[1])<<8 | uint32(octets[2])<<16 | uint32(octets[3])<<24
	return parsed, nil
}

func processName(processID uint32) string {
	name := fmt.Sprintf("PID %d", processID)
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err == nil {
		defer func() {
			if err := windows.CloseHandle(handle); err != nil {
				log.Printf("[Process Traffic] Close process handle: %v", err)
			}
		}()
		buffer := make([]uint16, 1024)
		size := uint32(len(buffer))
		result, _, _ := queryProcessImageName.Call(
			uintptr(handle),
			0,
			uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(unsafe.Pointer(&size)),
		)
		runtime.KeepAlive(buffer)
		if result != 0 {
			name = filepath.Base(windows.UTF16ToString(buffer[:size]))
		}
	}
	return name
}
