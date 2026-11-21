package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestRelaySquadSignaling(t *testing.T) {
	cfg := Config{
		ListenAddr:  "127.0.0.1:0",
		TunName:     "lagvex-test",
		Subnet:      netip.MustParsePrefix("10.99.0.0/24"),
		PSK:         []byte("test-psk-12345"),
		MTU:         1400,
		IdleTimeout: 10 * time.Second,
	}

	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create relay server: %v", err)
	}

	httpServer := httptest.NewServer(server.signalingHandler())
	defer httpServer.Close()
	baseURL := httpServer.URL

	// 1. Health check.
	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on health check, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// 2. Create Squad Room
	createBody, _ := json.Marshal(map[string]any{
		"code": "LGVX-RELAY",
		"name": "HostPlayer#1001",
		"game": "Valorant",
		"ping": 16,
		"isp":  "Fiber",
	})
	resp, err = http.Post(baseURL+"/squad/create", "application/json", bytes.NewReader(createBody))
	if err != nil {
		t.Fatalf("squad create failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on squad create, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// 3. Join Squad Room
	joinBody, _ := json.Marshal(map[string]any{
		"code": "LGVX-RELAY",
		"name": "Teammate#2002",
		"game": "Valorant",
		"ping": 22,
		"isp":  "Broadband",
	})
	resp, err = http.Post(baseURL+"/squad/join", "application/json", bytes.NewReader(joinBody))
	if err != nil {
		t.Fatalf("squad join failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on squad join, got %d", resp.StatusCode)
	}
	var joinResult struct {
		Members []RelaySquadMember `json:"members"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&joinResult)
	_ = resp.Body.Close()

	if len(joinResult.Members) != 2 {
		t.Fatalf("expected 2 members in squad room, got %d", len(joinResult.Members))
	}

	// 4. Query Room
	resp, err = http.Get(baseURL + "/squad/room?code=LGVX-RELAY")
	if err != nil {
		t.Fatalf("query room failed: %v", err)
	}
	var roomResult struct {
		Members []RelaySquadMember `json:"members"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&roomResult)
	_ = resp.Body.Close()

	if len(roomResult.Members) != 2 {
		t.Fatalf("expected 2 members from query room, got %d", len(roomResult.Members))
	}
}
