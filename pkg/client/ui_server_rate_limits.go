package client

import (
	"encoding/json"
	"math"
	"net/http"
)

const maxAdapterRateLimitMbps = 1_000_000

func (u *UIServer) handleLocalRouterRateLimit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !u.localRouter.Status().Available {
		writeJSONError(w, http.StatusNotImplemented, "per-adapter router limits are available on Windows only")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var request struct {
		InterfaceIndex int     `json:"interfaceIndex"`
		LimitMbps      float64 `json:"limitMbps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid adapter rate limit request: "+err.Error())
		return
	}
	if request.InterfaceIndex <= 0 {
		writeJSONError(w, http.StatusBadRequest, "a positive network interface index is required")
		return
	}
	if math.IsNaN(request.LimitMbps) || math.IsInf(request.LimitMbps, 0) ||
		request.LimitMbps < 0 || request.LimitMbps > maxAdapterRateLimitMbps {
		writeJSONError(w, http.StatusBadRequest, "limitMbps must be between 0 and 1000000")
		return
	}

	bytesPerSecond := uint64(math.Round(request.LimitMbps * 125_000))
	if err := u.configStore.SetAdapterRateCap(request.InterfaceIndex, bytesPerSecond); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "save adapter rate limit: "+err.Error())
		return
	}
	if err := u.localRouter.SetAdapterRateLimits(u.configStore.Get().AdapterRateCaps); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "apply adapter rate limit: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(u.routerStatus())
}
