//go:build windows

package client

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestIsPhysicalDownloadInterfaceType(t *testing.T) {
	tests := []struct {
		name   string
		ifType uint32
		want   bool
	}{
		{name: "ethernet", ifType: windows.IF_TYPE_ETHERNET_CSMACD, want: true},
		{name: "wifi", ifType: windows.IF_TYPE_IEEE80211, want: true},
		{name: "tunnel", ifType: windows.IF_TYPE_TUNNEL, want: false},
		{name: "other", ifType: windows.IF_TYPE_OTHER, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isPhysicalDownloadInterfaceType(test.ifType); got != test.want {
				t.Fatalf("isPhysicalDownloadInterfaceType(%d) = %v, want %v", test.ifType, got, test.want)
			}
		})
	}
}
