//go:build windows

package tun

import (
	"testing"
)

// tunnel is the tunnel adapter in every case below, at the metric
// PreferForMulticast pins it to over Windows' own route metric for 224.0.0.0/4.
var tunnel = claim{luid: 1, name: "192168", route: 256, metric: preferredMetric}

func TestContestMovesAnAdapterTiedWithTheTunnel(t *testing.T) {
	// The case this exists for: another virtual network pinned to the same
	// floor. Windows settles the tie without saying how, and measurement said
	// it settles it against the tunnel, so a tie has to be broken rather than
	// left alone.
	rival := claim{luid: 2, name: "Radmin VPN", route: 256, metric: 1}

	moves, held, err := contest([]claim{tunnel, rival}, tunnel.luid)
	if err != nil {
		t.Fatalf("contest: %v", err)
	}
	if held != "" {
		t.Errorf("held = %q, want nothing in the way", held)
	}
	if len(moves) != 1 {
		t.Fatalf("moves = %d, want 1", len(moves))
	}
	if moves[0].rival.luid != rival.luid {
		t.Errorf("moved %q, want %q", moves[0].rival.name, rival.name)
	}
	if moves[0].metric != 2 {
		t.Errorf("metric = %d, want 2", moves[0].metric)
	}
}

func TestContestLeavesAdaptersTheTunnelAlreadyBeats(t *testing.T) {
	// Ordinary Wi-Fi and Ethernet metrics. Moving these would cost somebody
	// their printers for nothing.
	claims := []claim{
		tunnel,
		{luid: 2, name: "Ethernet", route: 256, metric: 25},
		{luid: 3, name: "Wi-Fi", route: 256, metric: 35},
		{luid: 4, name: "vEthernet (Default Switch)", route: 256, metric: 5000},
	}

	moves, held, err := contest(claims, tunnel.luid)
	if err != nil {
		t.Fatalf("contest: %v", err)
	}
	if len(moves) != 0 {
		t.Errorf("moves = %v, want none", moves)
	}
	if held != "" {
		t.Errorf("held = %q, want nothing in the way", held)
	}
}

func TestContestWillNotMoveTheAdapterCarryingTheInternet(t *testing.T) {
	// A full-tunnel VPN holding both the multicast route and the way out. The
	// LAN list is worth a warning; somebody's connection is not worth taking.
	rival := claim{luid: 2, name: "Some VPN", route: 256, metric: 0, internet: true}

	moves, held, err := contest([]claim{tunnel, rival}, tunnel.luid)
	if err != nil {
		t.Fatalf("contest: %v", err)
	}
	if len(moves) != 0 {
		t.Errorf("moves = %v, want none", moves)
	}
	if held != "Some VPN" {
		t.Errorf("held = %q, want the adapter named", held)
	}
}

func TestContestPutsEveryRivalPastTheTunnel(t *testing.T) {
	// Route metrics are Windows' to choose and need not match, so the new
	// interface metric is worked out per rival rather than assumed to be two.
	claims := []claim{
		tunnel,
		{luid: 2, name: "Beats us outright", route: 100, metric: 0},
		{luid: 3, name: "Tied", route: 256, metric: 1},
		{luid: 4, name: "Odd route metric", route: 10, metric: 4},
	}

	moves, _, err := contest(claims, tunnel.luid)
	if err != nil {
		t.Fatalf("contest: %v", err)
	}
	if len(moves) != 3 {
		t.Fatalf("moves = %d, want 3", len(moves))
	}
	for _, m := range moves {
		moved := claim{route: m.rival.route, metric: m.metric}
		if moved.total() <= tunnel.total() {
			t.Errorf("%s would end on %d, not past the tunnel's %d", m.rival.name, moved.total(), tunnel.total())
		}
	}
}

func TestContestWithoutTheTunnelsOwnRoute(t *testing.T) {
	// Windows adds the route with the address. Reading the table before it has
	// is not a rival having won, and must not read as one.
	_, _, err := contest([]claim{{luid: 2, name: "Ethernet", route: 256, metric: 25}}, tunnel.luid)
	if err == nil {
		t.Fatal("contest succeeded with no route for the adapter, want an error")
	}
}

func TestClaimTotalAddsTheRouteAndInterfaceMetrics(t *testing.T) {
	// What Windows compares, and the reason neither number alone decides
	// anything.
	if got := (claim{route: 256, metric: 1}).total(); got != 257 {
		t.Errorf("total = %d, want 257", got)
	}
}
