//go:build windows

package client

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

func locateSteamExecutable() (string, error) {
	candidates := make([]string, 0, 6)
	for _, keyPath := range []string{
		`Software\Valve\Steam`,
		`Software\Wow6432Node\Valve\Steam`,
	} {
		key, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if installPath, _, err := key.GetStringValue("SteamPath"); err == nil && installPath != "" {
			candidates = append(candidates, filepath.Join(installPath, "steam.exe"))
		}
		if executablePath, _, err := key.GetStringValue("SteamExe"); err == nil && executablePath != "" {
			candidates = append(candidates, executablePath)
		}
		_ = key.Close()
	}

	for _, root := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
		if root != "" {
			candidates = append(candidates, filepath.Join(root, "Steam", "steam.exe"))
		}
	}

	return resolveSteamExecutable(candidates)
}
