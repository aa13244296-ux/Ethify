package client

import (
	"errors"
	"fmt"
	"strings"
)

func isLocalRouterSplitDefaultRoute(prefix string) bool {
	return prefix == fullTunnelPrefixes[0] || prefix == fullTunnelPrefixes[1]
}

func shouldDeleteLocalRouterRoute(prefix, interfaceAlias string) bool {
	return isLocalRouterSplitDefaultRoute(prefix) && strings.EqualFold(interfaceAlias, "Ethifi Local Router")
}

func installLocalDefaultRoutes(ifIndex uint32, add, remove func(string, uint32) error) ([]string, error) {
	if ifIndex == 0 {
		return nil, errors.New("local router interface index is unavailable")
	}

	var installed []string
	for _, prefix := range fullTunnelPrefixes {
		if err := add(prefix, ifIndex); err != nil {
			remaining, rollbackErr := rollbackLocalRoutes(installed, ifIndex, remove)
			return remaining, errors.Join(fmt.Errorf("add local default route %s: %w", prefix, err), rollbackErr)
		}
		installed = append(installed, prefix)
	}
	return installed, nil
}

func rollbackLocalRoutes(prefixes []string, ifIndex uint32, remove func(string, uint32) error) ([]string, error) {
	var remaining []string
	var errs []error
	for i := len(prefixes) - 1; i >= 0; i-- {
		if err := remove(prefixes[i], ifIndex); err != nil {
			remaining = append(remaining, prefixes[i])
			errs = append(errs, fmt.Errorf("remove local route %s: %w", prefixes[i], err))
		}
	}
	return remaining, errors.Join(errs...)
}

func removeLocalRoutes(prefixes []string, ifIndex uint32, remove func(string, uint32) error) error {
	_, err := rollbackLocalRoutes(prefixes, ifIndex, remove)
	return err
}
