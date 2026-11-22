//go:build linux

package client

import (
	"net"
	"strings"
)

func eligibleDownloadInterfaceIndices() (map[int]struct{}, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	eligible := make(map[int]struct{})
	for _, iface := range interfaces {
		if iface.Index <= 0 || iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isPhysicalLinuxInterfaceName(iface.Name) {
			eligible[iface.Index] = struct{}{}
		}
	}
	return eligible, nil
}

func isPhysicalLinuxInterfaceName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for _, excluded := range []string{
		"lo", "ethify", "docker", "veth", "br-", "virbr", "tun", "tap",
		"cni", "flannel", "tailscale", "wg", "podman",
	} {
		if name == excluded || strings.HasPrefix(name, excluded) {
			return false
		}
	}
	for _, prefix := range []string{"eth", "en", "wl", "ww", "usb", "lan"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return len(name) >= 2 && name[0] == 'e'
}
