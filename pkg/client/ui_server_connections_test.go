package client

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type testLocalRouter struct {
	status          localRouterStatus
	err             error
	adapterRateCaps map[int]uint64
}

func (r *testLocalRouter) Enable() error {
	if r.err == nil {
		r.status.Active = true
	}
	return r.err
}
func (r *testLocalRouter) Disable() error {
	if r.err == nil {
		r.status.Active = false
	}
	return r.err
}
func (r *testLocalRouter) SetAdapterRateLimits(limits map[int]uint64) error {
	r.adapterRateCaps = make(map[int]uint64, len(limits))
	for index, limit := range limits {
		r.adapterRateCaps[index] = limit
	}
	return r.err
}
func (r *testLocalRouter) Status() localRouterStatus { return r.status }
func (r *testLocalRouter) Close() error              { return r.Disable() }

func TestGameConnectionsEndpointShape(t *testing.T) {
	server := NewUIServer(nil, nil, "")
	handler := server.setupRoutes()

	req := httptest.NewRequest(http.MethodGet, "/api/game-connections", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		ScannedAt string `json:"scannedAt"`
		Supported bool   `json:"supported"`
		Note      string `json:"note"`
		Reason    string `json:"reason"`
		Games     []struct {
			GameID      string              `json:"gameId"`
			Name        string              `json:"name"`
			Connections []ProcessConnection `json:"connections"`
		} `json:"games"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.ScannedAt == "" {
		t.Fatalf("expected scannedAt to be set")
	}
	if res.Note == "" {
		t.Fatalf("expected the platform note to explain what is scanned")
	}
	if !res.Supported && res.Reason == "" {
		t.Fatalf("an unsupported platform must carry a reason")
	}
	if len(res.Games) == 0 {
		t.Fatalf("expected the game catalog to be reflected in the response")
	}
	for _, game := range res.Games {
		if game.GameID == "" || game.Name == "" {
			t.Fatalf("game entry missing identifiers: %+v", game)
		}
	}
}

func TestPeerRoutingToggleEndpoint(t *testing.T) {
	engine, err := NewEngine(nil)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	server := NewUIServer(engine, nil, "")
	handler := server.setupRoutes()

	for _, want := range []bool{true, false} {
		body, _ := json.Marshal(map[string]bool{"enabled": want})
		req := httptest.NewRequest(http.MethodPost, "/api/peer-routing/toggle", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var res struct {
			Success bool `json:"success"`
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !res.Success || res.Enabled != want {
			t.Fatalf("toggle wanted %v, got %+v", want, res)
		}
	}
}

func TestPeerRoutingToggleRequiresEngine(t *testing.T) {
	server := NewUIServer(nil, nil, "")
	handler := server.setupRoutes()

	req := httptest.NewRequest(http.MethodPost, "/api/peer-routing/toggle", bytes.NewReader([]byte(`{"enabled":true}`)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without an engine, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFullTunnelToggleEndpoint(t *testing.T) {
	engine, err := NewEngine(nil)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	server := NewUIServer(engine, nil, "")
	handler := server.setupRoutes()

	for _, want := range []bool{true, false} {
		body, _ := json.Marshal(map[string]bool{"enabled": want})
		req := httptest.NewRequest(http.MethodPost, "/api/full-tunnel/toggle", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var res struct {
			Success bool `json:"success"`
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !res.Success || res.Enabled != want {
			t.Fatalf("full tunnel toggle wanted %v, got %+v", want, res)
		}
	}

	// /api/status carries the flag even while disconnected.
	statusReq := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	statusRec := httptest.NewRecorder()
	handler.ServeHTTP(statusRec, statusReq)
	if !strings.Contains(statusRec.Body.String(), "fullTunnel") {
		t.Fatalf("status must expose fullTunnel, got %s", statusRec.Body.String())
	}
}

func TestLocalRouterToggleEndpoint(t *testing.T) {
	server := NewUIServer(nil, nil, "")
	router := &testLocalRouter{status: localRouterStatus{Available: true, Elevated: true}}
	server.localRouter = router
	handler := server.setupRoutes()

	req := httptest.NewRequest(http.MethodPost, "/api/local-router", bytes.NewBufferString(`{"enabled":true}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !router.status.Active {
		t.Fatal("expected router to be enabled")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/local-router", bytes.NewBufferString(`{"enabled":false}`))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || router.status.Active {
		t.Fatalf("expected router to be disabled, got status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestConnectRejectsActiveLocalRouter(t *testing.T) {
	server := NewUIServer(nil, nil, "")
	server.localRouter = &testLocalRouter{status: localRouterStatus{Active: true, Available: true}}
	handler := server.setupRoutes()
	body, _ := json.Marshal(map[string]string{
		"relayEndpoint": "192.0.2.1:4433",
		"psk":           "test",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/connect", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for conflicting tunnel, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLocalRouterRateLimitEndpointPersistsCapAndRemovesItAtZero(t *testing.T) {
	server := NewUIServer(nil, nil, "")
	server.configStore = NewConfigStore(filepath.Join(t.TempDir(), "user_config.json"))
	router := &testLocalRouter{status: localRouterStatus{Available: true}}
	server.localRouter = router
	handler := server.setupRoutes()

	request := httptest.NewRequest(http.MethodPost, "/api/local-router/rate-limit", strings.NewReader(
		`{"interfaceIndex":6,"limitMbps":12.5}`,
	))
	request.Header.Set(dashboardRequestHeader, "1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("set rate limit status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := server.configStore.Get().AdapterRateCaps[6]; got != 1_562_500 {
		t.Fatalf("stored bytes per second = %d, want 1562500", got)
	}
	if got := router.adapterRateCaps[6]; got != 1_562_500 {
		t.Fatalf("applied bytes per second = %d, want 1562500", got)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/local-router/rate-limit", strings.NewReader(
		`{"interfaceIndex":6,"limitMbps":0}`,
	))
	request.Header.Set(dashboardRequestHeader, "1")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("clear rate limit status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, exists := server.configStore.Get().AdapterRateCaps[6]; exists {
		t.Fatal("zero limit should remove the cap")
	}
	if _, exists := router.adapterRateCaps[6]; exists {
		t.Fatal("zero limit should remove the active cap")
	}
}

func TestLocalRouterRateLimitEndpointValidatesInput(t *testing.T) {
	server := NewUIServer(nil, nil, "")
	server.localRouter = &testLocalRouter{status: localRouterStatus{Available: true}}
	handler := server.setupRoutes()
	for _, body := range []string{
		`{"interfaceIndex":0,"limitMbps":5}`,
		`{"interfaceIndex":6,"limitMbps":-1}`,
		`{"interfaceIndex":6,"limitMbps":1000001}`,
		`{"interfaceIndex":6,"limitMbps":1e999}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/local-router/rate-limit", strings.NewReader(body))
		request.Header.Set(dashboardRequestHeader, "1")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("body %s status = %d, want %d", body, recorder.Code, http.StatusBadRequest)
		}
	}
}
