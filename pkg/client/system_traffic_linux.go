//go:build linux

package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const systemTrafficCountersSupported = true

func systemNetworkBindingReady() bool {
	return os.Geteuid() == 0 || linuxCapabilityEffective(unixCapNetRaw)
}

func systemNetworkBindingNote() string {
	if systemNetworkBindingReady() {
		return ""
	}
	return "Linux interface-bound transfers need root or CAP_NET_RAW. The system-wide router additionally needs CAP_NET_ADMIN."
}

func readSystemInterfaceByteCounters(networks []DownloadNetwork) (map[int]interfaceByteCounters, error) {
	return readLinuxInterfaceByteCounters("/sys/class/net", networks)
}

func readLinuxInterfaceByteCounters(root string, networks []DownloadNetwork) (map[int]interfaceByteCounters, error) {
	counters := make(map[int]interfaceByteCounters, len(networks))
	for _, network := range networks {
		readCounter := func(name string) (uint64, error) {
			path := filepath.Join(root, network.Name, "statistics", name)
			data, err := os.ReadFile(path)
			if err != nil {
				return 0, fmt.Errorf("read Linux %s counter for %s: %w", name, network.Name, err)
			}
			value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse Linux %s counter for %s: %w", name, network.Name, err)
			}
			return value, nil
		}
		received, err := readCounter("rx_bytes")
		if err != nil {
			return nil, err
		}
		sent, err := readCounter("tx_bytes")
		if err != nil {
			return nil, err
		}
		counters[network.InterfaceIndex] = interfaceByteCounters{received: received, sent: sent}
	}
	return counters, nil
}

func readTCPProcessCounters(_ []DownloadNetwork, _ map[string]struct{}) ([]processFlowCounter, error) {
	return nil, fmt.Errorf("per-process traffic counters are not available on Linux")
}
