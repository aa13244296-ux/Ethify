package client

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProxyLaunchEnvironmentOverridesExistingProxyVariables(t *testing.T) {
	environment := proxyLaunchEnvironment([]string{
		"PATH=C:\\Windows",
		"HTTP_PROXY=http://old-http-proxy",
		"https_proxy=http://old-https-proxy",
		"all_proxy=socks5://old-socks-proxy",
		"NO_PROXY=localhost",
	})
	values := make(map[string]string, len(environment))
	proxyCounts := make(map[string]int, len(proxyLaunchEnvironmentValues))
	for _, entry := range environment {
		key, value, found := strings.Cut(entry, "=")
		if found {
			normalizedKey := strings.ToUpper(key)
			values[normalizedKey] = value
			if isProxyLaunchEnvironmentKey(key) {
				proxyCounts[normalizedKey]++
			}
		}
	}

	for _, variable := range proxyLaunchEnvironmentValues {
		if got := values[variable.name]; got != variable.value {
			t.Errorf("%s = %q, want %q", variable.name, got, variable.value)
		}
		if got := proxyCounts[variable.name]; got != 1 {
			t.Errorf("%s occurs %d times in environment, want once", variable.name, got)
		}
	}
	if got := values["PATH"]; got != "C:\\Windows" {
		t.Errorf("PATH = %q, want original value", got)
	}
	if got := values["NO_PROXY"]; got != "localhost" {
		t.Errorf("NO_PROXY = %q, want original value", got)
	}
}

func TestResolveSteamExecutableUsesFirstExistingCandidate(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "steam.exe")
	if err := os.WriteFile(executable, []byte("test executable"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveSteamExecutable([]string{
		filepath.Join(dir, "missing.exe"),
		executable,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != executable {
		t.Fatalf("resolved Steam executable = %q, want %q", got, executable)
	}
}

func TestStartProxyAppPassesProxyEnvironmentToChild(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(t.TempDir(), "proxy-env.json")
	t.Setenv("ETHIFI_PROXY_CHILD_TEST", "1")
	t.Setenv("ETHIFI_PROXY_CHILD_RESULT", resultPath)

	pid, err := startProxyApp(executable, []string{"-test.run=^TestProxyLaunchChildProcess$"})
	if err != nil {
		t.Fatal(err)
	}
	if pid <= 0 {
		t.Fatalf("started child PID = %d, want positive PID", pid)
	}

	deadline := time.Now().Add(5 * time.Second)
	var lastReadErr error
	for {
		data, err := os.ReadFile(resultPath)
		if err == nil {
			var values map[string]string
			if err := json.Unmarshal(data, &values); err != nil {
				t.Fatal(err)
			}
			for _, variable := range proxyLaunchEnvironmentValues {
				if got := values[variable.name]; got != variable.value {
					t.Errorf("child %s = %q, want %q", variable.name, got, variable.value)
				}
			}
			return
		}
		lastReadErr = err
		if time.Now().After(deadline) {
			t.Fatalf("child did not write a readable proxy environment: %v", lastReadErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProxyLaunchChildProcess(t *testing.T) {
	if os.Getenv("ETHIFI_PROXY_CHILD_TEST") != "1" {
		return
	}

	values := make(map[string]string, len(proxyLaunchEnvironmentValues))
	for _, variable := range proxyLaunchEnvironmentValues {
		values[variable.name] = os.Getenv(variable.name)
	}
	data, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	resultPath := os.Getenv("ETHIFI_PROXY_CHILD_RESULT")
	temporaryPath := resultPath + ".tmp"
	if err := os.WriteFile(temporaryPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporaryPath, resultPath); err != nil {
		t.Fatal(err)
	}
}

func TestHandleProxyLaunchRequiresActiveProxy(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "app.exe")
	if err := os.WriteFile(executable, []byte("test executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewUIServer(nil, nil, "").setupRoutes()
	body, err := json.Marshal(map[string]string{"executable": executable})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/launch-proxy", bytes.NewReader(body))
	request.Header.Set(dashboardRequestHeader, "1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("launch without proxy status = %d, want %d: %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
}

func TestHandleProxyLaunchRejectsUnknownPreset(t *testing.T) {
	handler := NewUIServer(nil, nil, "").setupRoutes()
	request := httptest.NewRequest(http.MethodPost, "/api/launch-proxy", strings.NewReader(`{"preset":"unknown"}`))
	request.Header.Set(dashboardRequestHeader, "1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown preset status = %d, want %d: %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}
