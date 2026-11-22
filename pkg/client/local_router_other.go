//go:build !windows

package client

import "errors"

type unsupportedLocalRouter struct{}

func newLocalSystemRouter() localSystemRouter { return unsupportedLocalRouter{} }

func (unsupportedLocalRouter) Enable() error {
	return errors.New("the local system router is only available on Windows")
}
func (unsupportedLocalRouter) Disable() error { return nil }
func (unsupportedLocalRouter) SetAdapterRateLimits(map[int]uint64) error {
	return errors.New("the local router is not supported on this platform")
}
func (unsupportedLocalRouter) Status() localRouterStatus {
	return localRouterStatus{Available: false, Networks: []DownloadNetwork{}}
}
func (unsupportedLocalRouter) Close() error { return nil }
