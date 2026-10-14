package allocation

import (
	"testing"
	"time"
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
