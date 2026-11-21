package client

import "sort"

type localRouterStatus struct {
	Active          bool              `json:"active"`
	Available       bool              `json:"available"`
	Elevated        bool              `json:"elevated"`
	Networks        []DownloadNetwork `json:"networks"`
	ActiveEgress    uint64            `json:"activeEgress,omitempty"`
	EgressLimit     uint64            `json:"egressLimit,omitempty"`
	TCPFlows        uint64            `json:"tcpFlows,omitempty"`
	UDPFlows        uint64            `json:"udpFlows,omitempty"`
	TCPDialAttempts uint64            `json:"tcpDialAttempts,omitempty"`
	UDPDialAttempts uint64            `json:"udpDialAttempts,omitempty"`
	TCPConnected    uint64            `json:"tcpConnected,omitempty"`
	UDPConnected    uint64            `json:"udpConnected,omitempty"`
	LastError       string            `json:"lastError,omitempty"`
}

const localRouterEgressLimit = 512

type localSystemRouter interface {
	Enable() error
	Disable() error
	Status() localRouterStatus
	Close() error
}

func orderAdapterCandidates(networks []DownloadNetwork, active map[int]int, next int) []DownloadNetwork {
	return orderAdapterCandidatesByLoad(networks, active, nil, next)
}

func orderAdapterCandidatesByLoad(networks []DownloadNetwork, active map[int]int, bytesPerSecond map[int]float64, next int) []DownloadNetwork {
	if len(networks) == 0 {
		return nil
	}
	ordered := make([]DownloadNetwork, 0, len(networks))
	for offset := 0; offset < len(networks); offset++ {
		i := (next + offset) % len(networks)
		ordered = append(ordered, networks[i])
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if bytesPerSecond[left.InterfaceIndex] != bytesPerSecond[right.InterfaceIndex] {
			return bytesPerSecond[left.InterfaceIndex] < bytesPerSecond[right.InterfaceIndex]
		}
		return active[left.InterfaceIndex] < active[right.InterfaceIndex]
	})
	return ordered
}
