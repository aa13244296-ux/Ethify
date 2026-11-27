package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalRequestGuard(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := withLocalRequestGuard(next)

	tests := []struct {
		name       string
		method     string
		origin     string
		header     string
		path       string
		wantCORS   bool
		wantStatus int
	}{
		{name: "state change without header", method: http.MethodPost, wantStatus: http.StatusForbidden},
		{name: "state change with local origin but no header", method: http.MethodPost, origin: "http://127.0.0.1:18889", wantStatus: http.StatusForbidden},
		{name: "state change with local origin and header", method: http.MethodPost, origin: "http://127.0.0.1:18889", header: "1", wantStatus: http.StatusNoContent},
		{name: "localhost dev origin with header", method: http.MethodPost, origin: "http://localhost:5173", header: "1", wantStatus: http.StatusNoContent},
		{name: "internet origin is rejected even with header", method: http.MethodPost, origin: "https://evil.example", header: "1", wantStatus: http.StatusForbidden},
		{name: "opaque origin is rejected", method: http.MethodPost, origin: "null", header: "1", wantStatus: http.StatusForbidden},
		{name: "extension can start a browser download", method: http.MethodPost, origin: "chrome-extension://abcdefghijklmnopabcdefghijklmnop", path: "/api/downloads", wantCORS: true, wantStatus: http.StatusNoContent},
		{name: "extension can cancel a browser download", method: http.MethodPost, origin: "chrome-extension://abcdefghijklmnopabcdefghijklmnop", path: "/api/downloads/cancel", wantCORS: true, wantStatus: http.StatusNoContent},
		{name: "extension can discard an unstarted captured download", method: http.MethodPost, origin: "chrome-extension://abcdefghijklmnopabcdefghijklmnop", path: "/api/downloads/discard", wantCORS: true, wantStatus: http.StatusNoContent},
		{name: "extension can hand off a torrent file", method: http.MethodPost, origin: "chrome-extension://abcdefghijklmnopabcdefghijklmnop", path: "/api/torrents", wantCORS: true, wantStatus: http.StatusNoContent},
		{name: "extension cannot resume arbitrary torrent tasks", method: http.MethodPost, origin: "chrome-extension://abcdefghijklmnopabcdefghijklmnop", path: "/api/torrents/resume", wantStatus: http.StatusForbidden},
		{name: "extension can read adapter list", method: http.MethodGet, origin: "chrome-extension://abcdefghijklmnopabcdefghijklmnop", path: "/api/download-networks", wantCORS: true, wantStatus: http.StatusNoContent},
		{name: "extension can read download status", method: http.MethodGet, origin: "chrome-extension://abcdefghijklmnopabcdefghijklmnop", path: "/api/downloads", wantCORS: true, wantStatus: http.StatusNoContent},
		{name: "extension cannot change another endpoint", method: http.MethodPost, origin: "chrome-extension://abcdefghijklmnopabcdefghijklmnop", path: "/api/system-proxy", wantStatus: http.StatusForbidden},
		{name: "invalid extension ID is rejected", method: http.MethodPost, origin: "chrome-extension://zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", path: "/api/downloads", wantStatus: http.StatusForbidden},
		{name: "read-only request is not blocked", method: http.MethodGet, origin: "https://evil.example", wantStatus: http.StatusNoContent},
		{name: "cli request with header and no origin", method: http.MethodPost, header: "1", wantStatus: http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/test", nil)
			if tt.path != "" {
				req.URL.Path = tt.path
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.header != "" {
				req.Header.Set(dashboardRequestHeader, tt.header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantCORS && rec.Header().Get("Access-Control-Allow-Origin") != tt.origin {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q", rec.Header().Get("Access-Control-Allow-Origin"), tt.origin)
			}
		})
	}
}
