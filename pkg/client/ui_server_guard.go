package client

import (
	"net/http"
	"net/url"
	"strings"
)

// dashboardRequestHeader is required on every state-changing dashboard API
// request. A cross-site browser request cannot set a custom header without a
// CORS preflight, and the dashboard never grants CORS, so this blocks CSRF
// against the loopback API. Non-browser callers (curl, scripts, the HUD) must
// send it too; GET/HEAD/OPTIONS stay open because they are read-only here.
const dashboardRequestHeader = "X-Ethify-Request"

// isStateChangingMethod reports whether an HTTP method can modify state.
func isStateChangingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// isLocalOrigin accepts only loopback dashboards (including the Vite dev
// server on another port). Any internet origin is rejected even before the
// custom header check.
func isLocalOrigin(origin *url.URL) bool {
	if origin == nil {
		return false
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return false
	}
	host := strings.ToLower(origin.Hostname())
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func isBrowserExtensionOrigin(origin *url.URL) bool {
	if origin == nil || origin.Scheme != "chrome-extension" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	id := origin.Hostname()
	if origin.Host != id || len(id) != 32 {
		return false
	}
	for _, char := range id {
		if char < 'a' || char > 'p' {
			return false
		}
	}
	return true
}

func isBrowserExtensionRequest(origin *url.URL, method, path string) bool {
	if !isBrowserExtensionOrigin(origin) {
		return false
	}
	switch method {
	case http.MethodGet, http.MethodHead:
		return path == "/api/download-networks" || path == "/api/downloads"
	case http.MethodPost:
		return path == "/api/downloads" || path == "/api/downloads/cancel" || path == "/api/torrents"
	default:
		return false
	}
}

// withLocalRequestGuard protects state-changing dashboard endpoints from
// cross-site requests and from browsers that cannot prove intent with the
// custom header. The browser extension is limited to the read-only network and
// download status APIs, download start/cancel endpoints, and torrent creation.
func withLocalRequestGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			parsed, err := url.Parse(origin)
			if err == nil && isBrowserExtensionRequest(parsed, r.Method, r.URL.Path) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
				next.ServeHTTP(w, r)
				return
			}
			if isStateChangingMethod(r.Method) && (err != nil || !isLocalOrigin(parsed)) {
				writeGuardError(w, "cross-origin request rejected")
				return
			}
		}

		if !isStateChangingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		if r.Header.Get(dashboardRequestHeader) != "1" {
			writeGuardError(w, "missing "+dashboardRequestHeader+" header")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeGuardError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":"` + message + `"}`))
}
