package client

import (
	"encoding/json"
	"net/http"
)

func (u *UIServer) handleLocalProxy(w http.ResponseWriter, r *http.Request) {
	if u.localProxy == nil {
		u.localProxy = NewLocalProxyManager()
	}
	if u.systemProxy == nil {
		u.systemProxy = NewSystemProxyManager()
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(u.localProxy.Snapshot())
	case http.MethodPost:
		u.systemProxyMu.Lock()
		defer u.systemProxyMu.Unlock()
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		var request struct {
			Enabled          bool  `json:"enabled"`
			InterfaceIndices []int `json:"interfaceIndices"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid local proxy request: "+err.Error())
			return
		}
		if request.Enabled {
			if err := u.localProxy.Start(request.InterfaceIndices); err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
		} else {
			if err := u.systemProxy.Disable(); err != nil {
				writeJSONError(w, http.StatusConflict, err.Error())
				return
			}
			u.localProxy.Stop()
			u.systemProxyStartedLocalProxy = false
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(u.localProxy.Snapshot())
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
