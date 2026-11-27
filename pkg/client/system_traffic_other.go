//go:build !windows && !linux

package client

import "errors"

const systemTrafficCountersSupported = false

var errSystemTrafficUnsupported = errors.New("system-wide adapter counters are not supported on this platform")

func readSystemInterfaceByteCounters(_ []DownloadNetwork) (map[int]interfaceByteCounters, error) {
	return nil, errSystemTrafficUnsupported
}

func readTCPProcessCounters(_ []DownloadNetwork, _ map[string]struct{}) ([]processFlowCounter, error) {
	return nil, errSystemTrafficUnsupported
}

func systemNetworkBindingReady() bool { return true }

func systemNetworkBindingNote() string { return "" }
