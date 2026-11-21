//go:build windows

package client

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isTCPPingConnectionRefused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED)
}
