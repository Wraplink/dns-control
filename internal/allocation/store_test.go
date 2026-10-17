package allocation

import "testing"

func TestPollRespectsTypeCapacity(t *testing.T) {
	store := NewStore()
	store.EnsurePending("sni.example", TypeSNI)
	store.EnsurePending("route.example", TypeRoute)

	leases := store.Poll("edge-1", 10, 1, 0)
	if len(leases) != 1 {
		t.Fatalf("expected one lease, got %d", len(leases))
	}
	if leases[0].Type != TypeSNI {
		t.Fatalf("expected SNI lease, got %s", leases[0].Type)
	}
}

func TestFailedAssignmentReturnsToPending(t *testing.T) {
	store := NewStore()
	store.EnsurePending("example.com", TypeSNI)
	leases := store.Poll("edge-1", 1, 1, 0)
	if len(leases) != 1 {
		t.Fatal("expected assigned lease")
	}
	if !store.Fail(leases[0].ID, "edge-1") {
		t.Fatal("expected failure report to be accepted")
	}

	lease, ok := store.Get("example.com")
	if !ok {
		t.Fatal("lease not found")
	}
	if lease.State != StatePending || lease.AgentID != "" {
		t.Fatalf("expected recycled pending lease, got %+v", lease)
	}
}
