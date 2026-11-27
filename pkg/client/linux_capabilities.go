//go:build linux

package client

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

func linuxCapabilityEffective(capability uint) bool {
	if os.Geteuid() == 0 {
		return true
	}
	file, err := os.Open("/proc/self/status")
	if err != nil {
		return false
	}
	defer file.Close()

	const effectivePrefix = "CapEff:"
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, effectivePrefix) {
			continue
		}
		bits, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, effectivePrefix)), 16, 64)
		return err == nil && capability < 64 && bits&(uint64(1)<<capability) != 0
	}
	return false
}
