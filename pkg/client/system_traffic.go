package client

import (
	"sort"
	"sync"
	"time"
)

type systemTrafficSnapshot struct {
	Supported               bool                         `json:"supported"`
	NetworkBindingReady     bool                         `json:"networkBindingReady"`
	NetworkBindingNote      string                       `json:"networkBindingNote,omitempty"`
	Adapters                []systemAdapterTrafficSample `json:"adapters"`
	ProcessTrafficSupported bool                         `json:"processTrafficSupported"`
	ProcessTrafficNote      string                       `json:"processTrafficNote,omitempty"`
	Processes               []systemProcessTrafficSample `json:"processes"`
}

type systemAdapterTrafficSample struct {
	InterfaceIndex int    `json:"interfaceIndex"`
	Name           string `json:"name"`
	Address        string `json:"address"`
	DownRateBps    int64  `json:"downRateBps"`
	UpRateBps      int64  `json:"upRateBps"`
}

type systemProcessTrafficSample struct {
	ProcessID      uint32 `json:"processId"`
	Name           string `json:"name"`
	InterfaceIndex int    `json:"interfaceIndex"`
	InterfaceName  string `json:"interfaceName"`
	Connections    int    `json:"connections"`
	Downloaded     uint64 `json:"downloaded"`
	Uploaded       uint64 `json:"uploaded"`
	DownRateBps    int64  `json:"downRateBps"`
	UpRateBps      int64  `json:"upRateBps"`
}

type processFlowCounter struct {
	Key            string
	ProcessID      uint32
	ProcessName    string
	InterfaceIndex int
	Received       uint64
	Sent           uint64
}

type processTrafficKey struct {
	processID      uint32
	processName    string
	interfaceIndex int
}

type interfaceByteCounters struct {
	received uint64
	sent     uint64
}

type interfaceTrafficSample struct {
	counters interfaceByteCounters
	at       time.Time
}

type systemTrafficMeter struct {
	readMu            sync.Mutex
	mu                sync.Mutex
	previous          map[int]interfaceTrafficSample
	previousFlows     map[string]processFlowCounter
	enabledFlows      map[string]struct{}
	lastProcessSample time.Time
}

func newSystemTrafficMeter() *systemTrafficMeter {
	return &systemTrafficMeter{
		previous:      make(map[int]interfaceTrafficSample),
		previousFlows: make(map[string]processFlowCounter),
		enabledFlows:  make(map[string]struct{}),
	}
}

func (m *systemTrafficMeter) snapshot(networks []DownloadNetwork, counters map[int]interfaceByteCounters, now time.Time) []systemAdapterTrafficSample {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot := make([]systemAdapterTrafficSample, 0, len(networks))
	current := make(map[int]interfaceTrafficSample, len(networks))
	for _, network := range networks {
		counters, ok := counters[network.InterfaceIndex]
		if !ok {
			continue
		}
		current[network.InterfaceIndex] = interfaceTrafficSample{counters: counters, at: now}
		rate := systemAdapterTrafficSample{
			InterfaceIndex: network.InterfaceIndex,
			Name:           network.Name,
			Address:        network.Address,
		}
		if previous, ok := m.previous[network.InterfaceIndex]; ok {
			elapsed := now.Sub(previous.at).Seconds()
			if elapsed > 0 && counters.received >= previous.counters.received && counters.sent >= previous.counters.sent {
				rate.DownRateBps = int64(float64(counters.received-previous.counters.received) / elapsed)
				rate.UpRateBps = int64(float64(counters.sent-previous.counters.sent) / elapsed)
			}
		}
		snapshot = append(snapshot, rate)
	}
	m.previous = current
	return snapshot
}

func (m *systemTrafficMeter) read() (systemTrafficSnapshot, error) {
	m.readMu.Lock()
	defer m.readMu.Unlock()

	networks, err := ListDownloadNetworks()
	if err != nil {
		return systemTrafficSnapshot{}, err
	}
	counters, err := readSystemInterfaceByteCounters(networks)
	if err != nil {
		return systemTrafficSnapshot{}, err
	}
	now := time.Now()
	adapters := m.snapshot(networks, counters, now)
	flows, processErr := readTCPProcessCounters(networks, m.enabledFlows)
	if processErr != nil {
		return systemTrafficSnapshot{
			Supported:           true,
			NetworkBindingReady: systemNetworkBindingReady(),
			NetworkBindingNote:  systemNetworkBindingNote(),
			Adapters:            adapters,
			ProcessTrafficNote:  processErr.Error(),
			Processes:           []systemProcessTrafficSample{},
		}, nil
	}
	return systemTrafficSnapshot{
		Supported:               true,
		NetworkBindingReady:     systemNetworkBindingReady(),
		NetworkBindingNote:      systemNetworkBindingNote(),
		Adapters:                adapters,
		ProcessTrafficSupported: true,
		ProcessTrafficNote:      "Per-process TCP data only; UDP is included in adapter totals but cannot be attributed here.",
		Processes:               m.processSnapshot(networks, flows, now),
	}, nil
}

func (m *systemTrafficMeter) processSnapshot(
	networks []DownloadNetwork,
	flows []processFlowCounter,
	now time.Time,
) []systemProcessTrafficSample {
	m.mu.Lock()
	defer m.mu.Unlock()

	networkNames := make(map[int]string, len(networks))
	for _, network := range networks {
		networkNames[network.InterfaceIndex] = network.Name
	}
	currentFlows := make(map[string]processFlowCounter, len(flows))
	connectionCounts := make(map[processTrafficKey]int)
	for _, flow := range flows {
		currentFlows[flow.Key] = flow
		key := processTrafficKey{
			processID:      flow.ProcessID,
			processName:    flow.ProcessName,
			interfaceIndex: flow.InterfaceIndex,
		}
		connectionCounts[key]++
	}

	elapsed := time.Second
	if !m.lastProcessSample.IsZero() {
		elapsed = now.Sub(m.lastProcessSample)
	}
	if elapsed <= 0 {
		elapsed = time.Millisecond
	}
	samples := make(map[processTrafficKey]systemProcessTrafficSample)
	for key, flow := range currentFlows {
		group := processTrafficKey{
			processID:      flow.ProcessID,
			processName:    flow.ProcessName,
			interfaceIndex: flow.InterfaceIndex,
		}
		sample := samples[group]
		sample.ProcessID = flow.ProcessID
		sample.Name = flow.ProcessName
		sample.InterfaceIndex = flow.InterfaceIndex
		sample.InterfaceName = networkNames[flow.InterfaceIndex]
		sample.Connections = connectionCounts[group]
		previous, ok := m.previousFlows[key]
		if !ok || flow.Received < previous.Received || flow.Sent < previous.Sent {
			samples[group] = sample
			continue
		}
		received := flow.Received - previous.Received
		sent := flow.Sent - previous.Sent
		sample.DownRateBps += int64(float64(received) / elapsed.Seconds())
		sample.UpRateBps += int64(float64(sent) / elapsed.Seconds())
		sample.Downloaded += received
		sample.Uploaded += sent
		samples[group] = sample
	}

	m.previousFlows = currentFlows
	m.lastProcessSample = now
	snapshot := make([]systemProcessTrafficSample, 0, len(samples))
	for _, sample := range samples {
		snapshot = append(snapshot, sample)
	}
	sort.Slice(snapshot, func(i, j int) bool {
		left := snapshot[i].DownRateBps + snapshot[i].UpRateBps
		right := snapshot[j].DownRateBps + snapshot[j].UpRateBps
		if left != right {
			return left > right
		}
		return snapshot[i].Downloaded+snapshot[i].Uploaded > snapshot[j].Downloaded+snapshot[j].Uploaded
	})
	if len(snapshot) > 12 {
		snapshot = snapshot[:12]
	}
	return snapshot
}
