//go:build windows

package client

import "testing"

func TestWindowsUnicastInterfaceValueUsesNetworkByteOrder(t *testing.T) {
	if got, want := windowsUnicastInterfaceValue(1), 1<<24; got != want {
		t.Fatalf("Windows interface socket value = %#x, want %#x", got, want)
	}
}
