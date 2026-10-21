package allocation

import (
	"testing"

	"github.com/wraplink/dns-control/internal/domain"
)

func TestReconcilePoliciesReleasesStaleLeases(t *testing.T) {
	store := NewStore()
	store.ReconcilePolicies([]domain.Rule{{Domain: "old.example", Mode: domain.ModeHijack}})
	if len(store.List()) != 1 {
		t.Fatal("expected one lease")
	}
	oldID := store.List()[0].ID
	store.ReconcilePolicies([]domain.Rule{{Domain: "old.example", Mode: domain.ModeHijack}})
	if store.List()[0].ID != oldID {
		t.Fatal("unchanged policy must preserve lease")
	}
	store.ReconcilePolicies([]domain.Rule{{Domain: "old.example", Mode: domain.ModeRoute, Route: &domain.RoutePolicy{
		DestinationIP: "192.0.2.4", Protocol: "tcp",
	}, Assignment: &domain.RouteAssignment{AgentID: "edge-a", PublicIP: "192.0.2.10", Generation: 1}}})
	leases := store.List()
	if len(leases) != 1 || leases[0].ID == oldID || leases[0].Type != TypeRoute {
		t.Fatalf("changed policy must replace lease: %+v", leases)
	}
	store.ReconcilePolicies(nil)
	if len(store.List()) != 0 {
		t.Fatal("deleted domain must remove lease")
	}
}
