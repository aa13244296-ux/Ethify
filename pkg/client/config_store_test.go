package client

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aa13244296-ux/ethify/pkg/profiles"
)

func TestConfigStorePersistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ethify_cfg_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Errorf("failed to remove temp dir: %v", err)
		}
	})

	cfgPath := filepath.Join(tempDir, "user_config.json")
	cs := NewConfigStore(cfgPath)

	// 1. Set nickname
	cs.SetNickname("CyberSniper#9999")
	if cs.Get().Nickname != "CyberSniper#9999" {
		t.Fatalf("expected nickname CyberSniper#9999, got %s", cs.Get().Nickname)
	}

	// 2. Add custom game
	customGame := profiles.GameDefinition{
		ID:           "testgame",
		Name:         "Test Shooter",
		Category:     "FPS",
		ProcessNames: []string{"testshooter.exe"},
		Regions: []profiles.GameRegion{
			{ID: "asia", Name: "Asia-Pacific", CIDRs: []string{"1.1.1.0/24"}},
		},
	}
	cs.AddCustomGame(customGame)
	if len(cs.Get().CustomGames) != 1 || cs.Get().CustomGames[0].ID != "testgame" {
		t.Fatalf("expected 1 custom game with id testgame, got %+v", cs.Get().CustomGames)
	}

	// 3. Add custom relay
	customRelay := profiles.RelayEndpoint{
		ID:       "tokyo-test",
		Name:     "Tokyo Test Node",
		Endpoint: "192.0.2.1:51820",
		Location: "Tokyo, Japan",
		Tier:     "custom",
	}
	cs.AddCustomRelay(customRelay)
	if len(cs.Get().CustomRelays) != 1 || cs.Get().CustomRelays[0].ID != "tokyo-test" {
		t.Fatalf("expected 1 custom relay, got %+v", cs.Get().CustomRelays)
	}

	// 4. Update Tweaks
	cs.SetTweaks(map[string]bool{
		"tcpNoDelay":   false,
		"disableNagle": true,
	})
	if cs.Get().Tweaks["tcpNoDelay"] != false || cs.Get().Tweaks["disableNagle"] != true {
		t.Fatalf("tweaks were not properly updated: %+v", cs.Get().Tweaks)
	}

	// 5. Create a second instance pointing to the same path to verify reload
	cs2 := NewConfigStore(cfgPath)
	cfg2 := cs2.Get()
	if cfg2.Nickname != "CyberSniper#9999" {
		t.Fatalf("expected reloaded nickname CyberSniper#9999, got %s", cfg2.Nickname)
	}
	if len(cfg2.CustomGames) != 1 || cfg2.CustomGames[0].Name != "Test Shooter" {
		t.Fatalf("expected reloaded custom game Test Shooter, got %+v", cfg2.CustomGames)
	}
	if len(cfg2.CustomRelays) != 1 || cfg2.CustomRelays[0].Endpoint != "192.0.2.1:51820" {
		t.Fatalf("expected reloaded custom relay, got %+v", cfg2.CustomRelays)
	}
}

func TestConfigStoreMigratesLegacyAppData(t *testing.T) {
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)

	legacyDir := filepath.Join(appData, "Lagvex")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "user_config.json")
	if err := os.WriteFile(legacyPath, []byte(`{"nickname":"Saved nickname"}`), 0600); err != nil {
		t.Fatal(err)
	}

	store := NewConfigStore("")
	if got := store.Get().Nickname; got != "Saved nickname" {
		t.Fatalf("migrated nickname = %q, want %q", got, "Saved nickname")
	}
	if _, err := os.Stat(filepath.Join(appData, "Ethify", "user_config.json")); err != nil {
		t.Fatalf("new config file was not created: %v", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy config file should remain as a backup: %v", err)
	}
}

func TestConfigStorePersistsAndCopiesAdapterRateCaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user_config.json")
	store := NewConfigStore(path)
	if err := store.SetAdapterRateCap(7, 125_000); err != nil {
		t.Fatal(err)
	}

	got := store.Get()
	if got.AdapterRateCaps[7] != 125_000 {
		t.Fatalf("adapter cap = %d, want 125000", got.AdapterRateCaps[7])
	}
	got.AdapterRateCaps[7] = 1
	if store.Get().AdapterRateCaps[7] != 125_000 {
		t.Fatal("returned config must not expose the store's rate cap map")
	}
	if reloaded := NewConfigStore(path).Get().AdapterRateCaps[7]; reloaded != 125_000 {
		t.Fatalf("persisted adapter cap = %d, want 125000", reloaded)
	}
	if err := store.SetAdapterRateCap(7, 0); err != nil {
		t.Fatal(err)
	}
	if _, exists := NewConfigStore(path).Get().AdapterRateCaps[7]; exists {
		t.Fatal("zero rate must remove the persisted adapter cap")
	}
}

func TestAddRecentRelay(t *testing.T) {
	cs := NewConfigStore(filepath.Join(t.TempDir(), "user_config.json"))

	cs.AddRecentRelay("a:1")
	cs.AddRecentRelay("b:2")
	cs.AddRecentRelay("a:1") // dedupe and move to front
	cs.AddRecentRelay("c:3")
	cs.AddRecentRelay("d:4") // cap at three entries

	got := cs.Get().RecentRelays
	want := []string{"d:4", "c:3", "a:1"}
	if len(got) != len(want) {
		t.Fatalf("recent relays = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recent relays = %v, want %v", got, want)
		}
	}

	// Empty input is ignored.
	cs.AddRecentRelay("   ")
	if got := cs.Get().RecentRelays; len(got) != 3 {
		t.Fatalf("empty input must not change the list: %v", got)
	}
}

func TestRecordGameServer(t *testing.T) {
	tempDir := t.TempDir()
	cs := NewConfigStore(filepath.Join(tempDir, "user_config.json"))

	// A measurement without a positive RTT is never recorded: failed probes
	// must not fabricate history.
	cs.RecordGameServer("valorant", GameServerRecord{Host: "1.2.3.4", Port: 443, AvgMs: 0})
	if got := cs.GameServersFor("valorant"); len(got) != 0 {
		t.Fatalf("avgMs<=0 must be rejected, got %v", got)
	}

	for i := 0; i < gameServerHistoryCap+2; i++ {
		cs.RecordGameServer("valorant", GameServerRecord{
			Host:    fmt.Sprintf("10.0.0.%d", i),
			Port:    443,
			AvgMs:   float64(20 + i),
			Samples: 2,
		})
	}
	got := cs.GameServersFor("valorant")
	if len(got) != gameServerHistoryCap {
		t.Fatalf("history cap = %d entries, want %d", len(got), gameServerHistoryCap)
	}
	if got[0].Host != "10.0.0.9" {
		t.Fatalf("most recent entry should be first, got %s", got[0].Host)
	}

	// Re-measuring the same host moves it to the front and refreshes the RTT.
	cs.RecordGameServer("valorant", GameServerRecord{Host: "10.0.0.2", Port: 443, AvgMs: 55, Samples: 2})
	got = cs.GameServersFor("valorant")
	if got[0].Host != "10.0.0.2" || got[0].AvgMs != 55 {
		t.Fatalf("upsert failed: %+v", got[0])
	}
	if len(got) != gameServerHistoryCap {
		t.Fatalf("upsert must not grow the list: %d entries", len(got))
	}

	// Other games are unaffected.
	if got := cs.GameServersFor("cs2"); len(got) != 0 {
		t.Fatalf("unrelated game history should be empty, got %v", got)
	}
}
