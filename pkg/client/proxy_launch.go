package client

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var proxyLaunchEnvironmentValues = []struct {
	name  string
	value string
}{
	{name: "HTTP_PROXY", value: "http://127.0.0.1:8080"},
	{name: "HTTPS_PROXY", value: "http://127.0.0.1:8080"},
	{name: "ALL_PROXY", value: "socks5h://127.0.0.1:1080"},
}

func proxyLaunchEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+len(proxyLaunchEnvironmentValues))
	for _, entry := range environment {
		key, _, hasValue := strings.Cut(entry, "=")
		if hasValue && isProxyLaunchEnvironmentKey(key) {
			continue
		}
		result = append(result, entry)
	}
	for _, variable := range proxyLaunchEnvironmentValues {
		result = append(result, variable.name+"="+variable.value)
	}
	return result
}

func isProxyLaunchEnvironmentKey(key string) bool {
	for _, variable := range proxyLaunchEnvironmentValues {
		if strings.EqualFold(key, variable.name) {
			return true
		}
	}
	return false
}

func resolveSteamExecutable(candidates []string) (string, error) {
	var lastErr error
	for _, candidate := range candidates {
		candidate = filepath.Clean(strings.TrimSpace(candidate))
		if candidate == "." || candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				lastErr = fmt.Errorf("inspect Steam executable %q: %w", candidate, err)
			}
			continue
		}
		if !info.IsDir() {
			return candidate, nil
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", errors.New("Steam was not found; install Steam or enter its executable path")
}

func startProxyApp(executable string, args []string) (int, error) {
	command := exec.Command(executable, args...)
	command.Env = proxyLaunchEnvironment(os.Environ())
	if err := command.Start(); err != nil {
		return 0, fmt.Errorf("start %q: %w", executable, err)
	}

	pid := command.Process.Pid
	if err := command.Process.Release(); err != nil {
		return pid, fmt.Errorf("app started as process %d, but its process handle could not be released: %w", pid, err)
	}
	return pid, nil
}
