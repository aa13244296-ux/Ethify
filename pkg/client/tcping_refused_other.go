//go:build !windows

package client

import (
	"errors"
	"syscall"
)

func isTCPPingConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
