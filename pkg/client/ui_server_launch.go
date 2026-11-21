package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (u *UIServer) handleProxyLaunch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var request struct {
		Executable string `json:"executable"`
		Preset     string `json:"preset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid proxy launch request: "+err.Error())
		return
	}

	request.Executable = strings.TrimSpace(request.Executable)
	request.Preset = strings.ToLower(strings.TrimSpace(request.Preset))
	if request.Executable != "" && request.Preset != "" {
		writeJSONError(w, http.StatusBadRequest, "choose either an executable path or a preset")
		return
	}

	executable := request.Executable
	if request.Preset != "" {
		if request.Preset != "steam" {
			writeJSONError(w, http.StatusBadRequest, "unknown proxy launch preset")
			return
		}
		var err error
		executable, err = locateSteamExecutable()
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
	} else {
		if executable == "" {
			writeJSONError(w, http.StatusBadRequest, "enter an absolute executable path")
			return
		}
		if !filepath.IsAbs(executable) {
			writeJSONError(w, http.StatusBadRequest, "executable path must be absolute")
			return
		}
		info, err := os.Stat(executable)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeJSONError(w, http.StatusNotFound, "executable was not found")
				return
			}
			writeJSONError(w, http.StatusBadRequest, "could not inspect executable: "+err.Error())
			return
		}
		if info.IsDir() {
			writeJSONError(w, http.StatusBadRequest, "executable path points to a directory")
			return
		}
	}

	if u.localProxy == nil {
		u.localProxy = NewLocalProxyManager()
	}
	u.systemProxyMu.Lock()
	defer u.systemProxyMu.Unlock()
	if !u.localProxy.Snapshot().Running {
		writeJSONError(w, http.StatusConflict, "start the local multi-adapter proxy before launching an app through it")
		return
	}

	pid, err := startProxyApp(executable, nil)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Started    bool   `json:"started"`
		ProcessID  int    `json:"processId"`
		Executable string `json:"executable"`
		Preset     string `json:"preset,omitempty"`
	}{
		Started:    true,
		ProcessID:  pid,
		Executable: executable,
		Preset:     request.Preset,
	})
}
