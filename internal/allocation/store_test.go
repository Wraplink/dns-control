package allocation

import (
	"testing"
	"time"

	"github.com/wraplink/dns-control/internal/domain"
)

func TestPollRespectsTypeCapacity(t *testing.T) {
	store := NewStore(time.Minute)
	store.EnsurePending("one.example", TypeSNI)
	store.EnsurePending("two.example", TypeSNI)
	store.EnsurePending("route.example", TypeRoute)

	sni := store.Poll("edge-a", 1, 0)
	if len(sni) != 1 || sni[0].Type != TypeSNI {
		t.Fatalf("expected one SNI lease, got %#v", sni)
	}

	routes := store.Poll("edge-b", 0, 1)
	if len(routes) != 1 || routes[0].Type != TypeRoute {
		t.Fatalf("expected one route lease, got %#v", routes)
	}
}

func TestExpiredActiveLeaseIsRequeued(t *testing.T) {
	store := NewStore(15 * time.Millisecond)
	store.EnsurePending("game.example", TypeSNI)

	assigned := store.Poll("edge-a", 1, 0)
	if len(assigned) != 1 {
		t.Fatalf("expected one assigned lease, got %d", len(assigned))
	}
	leaseID := assigned[0].ID

	if !store.Activate(leaseID, "edge-a", "192.0.2.10", "game.example", "") {
		t.Fatal("expected activation to succeed")
	}

	time.Sleep(25 * time.Millisecond)
	if _, ok := store.Get("game.example"); ok {
		t.Fatal("expired active lease must not be returned for DNS")
	}

	if changed := store.ReconcileExpired(); changed != 1 {
		t.Fatalf("expected one reconciled lease, got %d", changed)
	}

	reassigned := store.Poll("edge-b", 1, 0)
	if len(reassigned) != 1 || reassigned[0].ID != leaseID {
		t.Fatalf("expected same lease to be reassigned, got %#v", reassigned)
	}
}

func TestHeartbeatReturnsRevokedLease(t *testing.T) {
	store := NewStore(time.Minute)
	store.EnsurePending("game.example", TypeSNI)
	assigned := store.Poll("edge-a", 1, 0)
	if len(assigned) != 1 {
		t.Fatal("expected one lease")
	}

	release := store.Heartbeat("edge-b", []string{assigned[0].ID})
	if len(release) != 1 || release[0] != assigned[0].ID {
		t.Fatalf("expected lease revocation, got %#v", release)
	}
}

func TestFailureReturnsLeaseToScheduler(t *testing.T) {
	store := NewStore(time.Minute)
	store.EnsurePending("game.example", TypeSNI)
	assigned := store.Poll("edge-a", 1, 0)
	if len(assigned) != 1 {
		t.Fatal("expected one lease")
	}

	if !store.Fail(assigned[0].ID, "edge-a") {
		t.Fatal("expected failure report to be accepted")
	}

	reassigned := store.Poll("edge-b", 1, 0)
	if len(reassigned) != 1 || reassigned[0].ID != assigned[0].ID {
		t.Fatalf("expected failed lease to be rescheduled, got %#v", reassigned)
	}
}

func TestAuthoritativeRouteAssignmentRequiresMatchingAgentIPAndGeneration(t *testing.T) {
	store := NewStore(time.Minute)
	store.ReconcilePolicies([]domain.Rule{{
		Domain:     "route.example",
		Mode:       domain.ModeRoute,
		Route:      &domain.RoutePolicy{DestinationIP: "198.51.100.10", Protocol: "tcp_udp"},
		Assignment: &domain.RouteAssignment{AgentID: "edge-a", PublicIP: "192.0.2.10", Generation: 7, State: "pending"},
	}})
	if got := store.Poll("edge-b", 0, 1); len(got) != 0 {
		t.Fatalf("wrong agent received route: %+v", got)
	}
	leases := store.Poll("edge-a", 0, 1)
	if len(leases) != 1 || leases[0].Address != "192.0.2.10" || leases[0].Generation != 7 {
		t.Fatalf("assigned route mismatch: %+v", leases)
	}
	id := leases[0].ID
	if store.ActivateWithGeneration(id, "edge-a", "192.0.2.10", "", "route-1", 6) {
		t.Fatal("stale generation must be rejected")
	}
	if store.ActivateWithGeneration(id, "edge-a", "192.0.2.11", "", "route-1", 7) {
		t.Fatal("wrong public IPv4 must be rejected")
	}
	if _, ok := store.Get("route.example"); ok {
		t.Fatal("route must not resolve before valid acknowledgement")
	}
	if !store.ActivateWithGeneration(id, "edge-a", "192.0.2.10", "", "route-1", 7) {
		t.Fatal("matching acknowledgement rejected")
	}
	if _, ok := store.Get("route.example"); !ok {
		t.Fatal("acknowledged route should be active")
	}
}

func TestRouteWithoutBackendAssignmentFailsClosed(t *testing.T) {
	store := NewStore(time.Minute)
	store.ReconcilePolicies([]domain.Rule{{
		Domain: "route.example",
		Mode:   domain.ModeRoute,
		Route:  &domain.RoutePolicy{DestinationIP: "198.51.100.10", Protocol: "tcp"},
	}})
	if got := store.Poll("edge-a", 0, 1); len(got) != 0 {
		t.Fatalf("unassigned route was dispatched: %+v", got)
	}
}

func TestAssignmentChangeRevokesOldLeaseAndRejectsStaleAck(t *testing.T) {
	store := NewStore(time.Minute)
	rule := domain.Rule{
		Domain:     "change.example",
		Mode:       domain.ModeRoute,
		Route: &domain.RoutePolicy{DestinationIP: "198.51.100.10", Protocol: "tcp"},
		Assignment: &domain.RouteAssignment{AgentID: "edge-a", PublicIP: "192.0.2.10", Generation: 1},
	}
	store.ReconcilePolicies([]domain.Rule{rule})
	first := store.Poll("edge-a", 0, 1)
	if len(first) != 1 {
		t.Fatalf("expected initial route, got %+v", first)
	}
	rule.Assignment = &domain.RouteAssignment{AgentID: "edge-b", PublicIP: "192.0.2.11", Generation: 2}
	store.ReconcilePolicies([]domain.Rule{rule})
	if store.ActivateWithGeneration(first[0].ID, "edge-a", "192.0.2.10", "", "old", 1) {
		t.Fatal("revoked lease accepted a stale acknowledgement")
	}
	if got := store.Heartbeat("edge-a", []string{first[0].ID}); len(got) != 1 {
		t.Fatalf("old agent must release revoked lease, got %+v", got)
	}
	if got := store.Poll("edge-a", 0, 1); len(got) != 0 {
		t.Fatalf("old agent must not receive new generation: %+v", got)
	}
	next := store.Poll("edge-b", 0, 1)
	if len(next) != 1 || next[0].Generation != 2 || next[0].Address != "192.0.2.11" {
		t.Fatalf("expected reassigned generation 2, got %+v", next)
	}
}
