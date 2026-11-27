//go:build !windows

package client

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

func openTransferPath(path string, _ bool) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	return exec.Command(command, path).Start()
}

func chooseTransferDirectory() (string, error) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("osascript", "-e", "POSIX path of (choose folder)")
	case "linux":
		command = exec.Command("zenity", "--file-selection", "--directory", "--title=Choose download folder")
	default:
		return "", errors.New("folder selection is not supported on this platform")
	}
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", errors.New("folder selection was cancelled")
	}
	return path, nil
}
