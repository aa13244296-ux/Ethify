//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

func shutdownWhenWindowClosed() bool {
	return false
}

func openDesktopWindow(url string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", url).Start()
	case "linux":
		_ = exec.Command("xdg-open", url).Start()
	}
}
