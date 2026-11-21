package client

import (
	"encoding/json"
	"net/http"
)

func (u *UIServer) handleTorrents(w http.ResponseWriter, r *http.Request) {
	if u.torrents == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "torrent manager is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(u.torrents.List())
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		var request struct {
			Source           string `json:"source"`
			InterfaceIndices []int  `json:"interfaceIndices"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid torrent request: "+err.Error())
			return
		}
		snapshot, err := u.torrents.Start(r.Context(), request.Source, request.InterfaceIndices)
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

func (u *UIServer) handleCancelTorrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if u.torrents == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "torrent manager is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var request struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid torrent cancel request: "+err.Error())
		return
	}
	if err := u.torrents.Cancel(request.ID); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (u *UIServer) handleResumeTorrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if u.torrents == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "torrent manager is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var request struct {
		ID               string `json:"id"`
		InterfaceIndices []int  `json:"interfaceIndices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid torrent resume request: "+err.Error())
		return
	}
	snapshot, err := u.torrents.Resume(r.Context(), request.ID, request.InterfaceIndices)
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (u *UIServer) handleDiscardTorrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if u.torrents == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "torrent manager is unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var request struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid torrent discard request: "+err.Error())
		return
	}
	if err := u.torrents.Discard(request.ID); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}
