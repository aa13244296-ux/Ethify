//go:build !windows

package client

import (
	"os"
	"path/filepath"
)

func locateSteamExecutable() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return resolveSteamExecutable([]string{
		filepath.Join(home, ".steam", "steam.exe"),
		filepath.Join(home, ".local", "share", "Steam", "steam.exe"),
	})
}
