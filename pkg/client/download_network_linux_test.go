//go:build linux

package client

import "testing"

func TestPhysicalLinuxInterfaceName(t *testing.T) {
	for _, name := range []string{"eth0", "enp3s0", "wlan0", "wlp2s0", "wwan0"} {
		if !isPhysicalLinuxInterfaceName(name) {
			t.Fatalf("physical interface %q was rejected", name)
		}
	}
	for _, name := range []string{"lo", "docker0", "veth123", "br-123", "virbr0", "ethify-tun0", "tailscale0", "tun0"} {
		if isPhysicalLinuxInterfaceName(name) {
			t.Fatalf("virtual interface %q was accepted", name)
		}
	}
}
