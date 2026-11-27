//go:build linux

package client

import (
	"os"
	"strconv"
	"strings"
)

func linuxCapabilityEffective(capability uint) bool {
	if os.Geteuid() == 0 {
		return true
	}
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}

	const effectivePrefix = "CapEff:"
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, effectivePrefix) {
			continue
		}
		bits, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, effectivePrefix)), 16, 64)
		return err == nil && capability < 64 && bits&(uint64(1)<<capability) != 0
	}
	return false
}
