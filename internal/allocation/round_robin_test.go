package allocation

import (
	"testing"
	"time"

	"github.com/wraplink/dns-control/internal/domain"
)

func TestRoundRobinReplicasAndExpiry(t *testing.T) {
	s := NewStore(time.Minute)
	s.ReconcilePolicies([]domain.Rule{{Domain: "example.com", Mode: domain.ModeHijack}})
	leases := s.Poll("agent-a", 1, 0)
	if len(leases) != 1 {
		t.Fatalf("expected one initial lease, got %d", len(leases))
	}
	if !s.Activate(leases[0].ID, "agent-a", "192.0.2.10", "example.com", "") {
		t.Fatal("activate primary")
	}
	if !s.AddActiveReplica("example.com", "agent-b", "192.0.2.20") {
		t.Fatal("add replica")
	}
	if s.AddActiveReplica("example.com", "agent-b", "192.0.2.20") {
		t.Fatal("duplicate replica accepted")
	}
	for i, want := range []string{"192.0.2.10", "192.0.2.20", "192.0.2.10", "192.0.2.20"} {
		got, ok := s.NextCandidate("example.com", nil)
		if !ok || got.Address != want {
			t.Fatalf("query %d: got %v, want %s", i, got, want)
		}
	}
	s.ReconcilePolicies([]domain.Rule{{Domain: "example.com", Mode: domain.ModeHijack}})
	if got := len(s.ActiveCandidates("example.com")); got != 2 {
		t.Fatalf("snapshot removed replicas: %d", got)
	}
	if _, ok := s.NextCandidate("example.com", func(l Lease) bool { return l.AgentID == "agent-b" }); !ok {
		t.Fatal("filtered replica unavailable")
	}
	s.ReconcilePolicies(nil)
	if got := len(s.ActiveCandidates("example.com")); got != 0 {
		t.Fatalf("deleted policy left %d allocations", got)
	}
}
