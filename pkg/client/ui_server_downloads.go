package client

import (
	"encoding/json"
	"net/http"
)

func (u *UIServer) handleDownloadNetworks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	networks, err := ListDownloadNetworks()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(networks)
}

func (u *UIServer) handleSystemTraffic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !systemTrafficCountersSupported {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(systemTrafficSnapshot{
			Supported:          false,
			Adapters:           []systemAdapterTrafficSample{},
			Processes:          []systemProcessTrafficSample{},
			ProcessTrafficNote: "Per-app traffic details are available on Windows only.",
		})
		return
	}
	if u.systemTraffic == nil {
		u.systemTraffic = newSystemTrafficMeter()
	}
	snapshot, err := u.systemTraffic.read()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (u *UIServer) handleDownloads(w http.ResponseWriter, r *http.Request) {
	if u.downloads == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "download manager is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(u.downloads.List())
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		var request struct {
			URL              string `json:"url"`
			InterfaceIndices []int  `json:"interfaceIndices"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid download request: "+err.Error())
			return
		}
		snapshot, err := u.downloads.Start(r.Context(), request.URL, request.InterfaceIndices)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(snapshot)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (u *UIServer) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if u.downloads == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "download manager is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var request struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid cancel request: "+err.Error())
		return
	}
	if err := u.downloads.Cancel(request.ID); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (u *UIServer) handleResumeDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if u.downloads == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "download manager is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var request struct {
		ID               string `json:"id"`
		InterfaceIndices []int  `json:"interfaceIndices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid resume request: "+err.Error())
		return
	}
	snapshot, err := u.downloads.Resume(r.Context(), request.ID, request.InterfaceIndices)
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (u *UIServer) handleDiscardDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if u.downloads == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "download manager is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var request struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid discard request: "+err.Error())
		return
	}
	if err := u.downloads.Discard(request.ID); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}
