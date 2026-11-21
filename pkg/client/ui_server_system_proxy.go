package client

import (
	"encoding/json"
	"errors"
	"net/http"
)

func (u *UIServer) handleSystemProxy(w http.ResponseWriter, r *http.Request) {
	if u.systemProxy == nil {
		u.systemProxy = NewSystemProxyManager()
	}
	if u.localProxy == nil {
		u.localProxy = NewLocalProxyManager()
	}

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(u.systemProxy.Snapshot())
	case http.MethodPost:
		u.systemProxyMu.Lock()
		defer u.systemProxyMu.Unlock()

		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		var request struct {
			Enabled          bool  `json:"enabled"`
			InterfaceIndices []int `json:"interfaceIndices"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid Windows system proxy request: "+err.Error())
			return
		}

		if request.Enabled {
			status := u.systemProxy.Snapshot()
			if status.Enabled && !status.Managed {
				writeJSONError(w, http.StatusConflict, "Windows already uses Ethifi's proxy address without a saved original setting")
				return
			}
			startedHere := !u.localProxy.Snapshot().Running
			if err := u.localProxy.Start(request.InterfaceIndices); err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			if !u.localProxy.Snapshot().Running {
				writeJSONError(w, http.StatusInternalServerError, "local proxy did not start")
				return
			}
			if err := u.systemProxy.Enable(); err != nil {
				restoreErr := u.systemProxy.Restore()
				if startedHere && restoreErr == nil {
					u.localProxy.Stop()
				} else if startedHere {
					u.systemProxyStartedLocalProxy = true
				}
				writeJSONError(w, http.StatusConflict, errors.Join(err, restoreErr).Error())
				return
			}
			if startedHere {
				u.systemProxyStartedLocalProxy = true
			}
		} else {
			if err := u.systemProxy.Disable(); err != nil {
				writeJSONError(w, http.StatusConflict, err.Error())
				return
			}
			if u.systemProxyStartedLocalProxy {
				u.localProxy.Stop()
				u.systemProxyStartedLocalProxy = false
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(u.systemProxy.Snapshot())
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
