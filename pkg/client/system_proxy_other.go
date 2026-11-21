//go:build !windows

package client

func NewSystemProxyManager() *SystemProxyManager {
	return newSystemProxyManager(nil, false, "")
}
