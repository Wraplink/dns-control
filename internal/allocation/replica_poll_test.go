package allocation

import (
	"testing"
	"time"

	"github.com/wraplink/dns-control/internal/domain"
)

func TestPollProvisionsAdditionalSNIAgents(t *testing.T) {
	s := NewStore(time.Minute)
	s.ReconcilePolicies([]domain.Rule{{Domain: "example.com", Mode: domain.ModeHijack}})
	first := s.Poll("agent-a", 1, 0)
	if len(first) != 1 {
		t.Fatalf("first poll: %d leases", len(first))
	}
	if !s.Activate(first[0].ID, "agent-a", "192.0.2.10", "example.com", "") {
		t.Fatal("primary activation failed")
	}
	second := s.Poll("agent-b", 1, 0)
	if len(second) != 1 || second[0].AgentID != "agent-b" || second[0].Domain != "example.com" {
		t.Fatalf("second agent did not receive replica: %+v", second)
	}
	if got := s.Poll("agent-b", 1, 0); len(got) != 0 {
		t.Fatalf("duplicate replica: %+v", got)
	}
	if !s.Activate(second[0].ID, "agent-b", "192.0.2.20", "example.com", "") {
		t.Fatal("replica activation failed")
	}
	for i, want := range []string{"192.0.2.10", "192.0.2.20", "192.0.2.10", "192.0.2.20"} {
		got, ok := s.NextCandidate("example.com", nil)
		if !ok || got.Address != want {
			t.Fatalf("round robin %d: got %+v, want %s", i, got, want)
		}
	}
	if got := s.Poll("agent-b", 1, 0); len(got) != 0 {
		t.Fatalf("active agent duplicated: %+v", got)
	}
}
