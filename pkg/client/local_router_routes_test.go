package client

import (
	"errors"
	"testing"
	"time"
)

func TestInstallLocalDefaultRoutesRollsBackOnFailure(t *testing.T) {
	var added, removed []string
	wantErr := errors.New("route add failed")
	routes, err := installLocalDefaultRoutes(12, func(prefix string, _ uint32) error {
		added = append(added, prefix)
		if len(added) == 2 {
			return wantErr
		}
		return nil
	}, func(prefix string, _ uint32) error {
		removed = append(removed, prefix)
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected route add error, got %v", err)
	}
	if len(routes) != 0 || len(added) != 2 || len(removed) != 1 || removed[0] != fullTunnelPrefixes[0] {
		t.Fatalf("unexpected route transaction: routes=%v added=%v removed=%v", routes, added, removed)
	}
}

func TestRemoveLocalRoutesReportsFailedCleanup(t *testing.T) {
	wantErr := errors.New("route delete failed")
	err := removeLocalRoutes(fullTunnelPrefixes, 12, func(prefix string, _ uint32) error {
		if prefix == fullTunnelPrefixes[0] {
			return wantErr
		}
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected cleanup error, got %v", err)
	}
}

func TestOrderAdapterCandidatesBalancesActiveFlows(t *testing.T) {
	networks := []DownloadNetwork{
		{InterfaceIndex: 6, Name: "Ethernet"},
		{InterfaceIndex: 13, Name: "Wi-Fi"},
	}
	active := map[int]int{6: 2, 13: 1}
	ordered := orderAdapterCandidates(networks, active, 0)
	if len(ordered) != 2 || ordered[0].InterfaceIndex != 13 || ordered[1].InterfaceIndex != 6 {
		t.Fatalf("expected the least-active adapter first, got %+v", ordered)
	}

	active[13] = 2
	ordered = orderAdapterCandidates(networks, active, 1)
	if len(ordered) != 2 || ordered[0].InterfaceIndex != 13 || ordered[1].InterfaceIndex != 6 {
		t.Fatalf("expected round-robin tie breaking, got %+v", ordered)
	}
}

func TestOrderAdapterCandidatesPrefersLowerMeasuredThroughput(t *testing.T) {
	networks := []DownloadNetwork{
		{InterfaceIndex: 6, Name: "Ethernet"},
		{InterfaceIndex: 13, Name: "Wi-Fi"},
	}
	ordered := orderAdapterCandidatesByLoad(
		networks,
		map[int]int{6: 0, 13: 0},
		map[int]float64{6: 400_000, 13: 0},
		0,
	)
	if len(ordered) != 2 || ordered[0].InterfaceIndex != 13 {
		t.Fatalf("expected low-throughput Wi-Fi first, got %+v", ordered)
	}

	ordered = orderAdapterCandidatesByLoad(
		networks,
		map[int]int{6: 0, 13: 3},
		map[int]float64{6: 0, 13: 0},
		0,
	)
	if len(ordered) != 2 || ordered[0].InterfaceIndex != 6 {
		t.Fatalf("expected the less-busy Ethernet first when throughput ties, got %+v", ordered)
	}
}

func TestOrderAdapterCandidatesUsesShortFailureCooldown(t *testing.T) {
	networks := []DownloadNetwork{
		{InterfaceIndex: 6, Name: "Ethernet"},
		{InterfaceIndex: 13, Name: "Wi-Fi"},
	}
	now := time.Now()
	failedAt := map[int]time.Time{13: now.Add(-time.Second)}
	active := map[int]int{}
	rates := map[int]float64{6: 100}

	ordered := orderAdapterCandidatesWithCooldown(networks, active, rates, failedAt, now, 4*time.Second, 0)
	if len(ordered) != 2 || ordered[0].InterfaceIndex != 6 || ordered[1].InterfaceIndex != 13 {
		t.Fatalf("recently failed adapter should be deprioritized, got %+v", ordered)
	}

	ordered = orderAdapterCandidatesWithCooldown(networks, active, rates, failedAt, now.Add(5*time.Second), 4*time.Second, 0)
	if len(ordered) != 2 || ordered[0].InterfaceIndex != 13 || ordered[1].InterfaceIndex != 6 {
		t.Fatalf("recovered adapter should rejoin load balancing after cooldown, got %+v", ordered)
	}
}

func TestLocalRouterStaleRouteMatchIsExact(t *testing.T) {
	for _, prefix := range fullTunnelPrefixes {
		if !isLocalRouterSplitDefaultRoute(prefix) {
			t.Fatalf("expected split-default route %q to match", prefix)
		}
	}
	if !shouldDeleteLocalRouterRoute(fullTunnelPrefixes[0], "Ethifi Local Router") {
		t.Fatal("expected Ethifi split-default route to be eligible for cleanup")
	}
	for _, test := range []struct {
		prefix string
		alias  string
	}{
		{prefix: fullTunnelPrefixes[0], alias: "Wi-Fi"},
		{prefix: "0.0.0.0/0", alias: "Ethifi Local Router"},
	} {
		if shouldDeleteLocalRouterRoute(test.prefix, test.alias) {
			t.Fatalf("unexpected stale route match for %q on %q", test.prefix, test.alias)
		}
	}
	for _, prefix := range []string{"0.0.0.0/0", "10.0.0.0/8", "128.0.0.0/2"} {
		if isLocalRouterSplitDefaultRoute(prefix) {
			t.Fatalf("unexpected route match for %q", prefix)
		}
	}
}
