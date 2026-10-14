package allocation

import (
	"testing"
	"time"
)

func TestEnsureRoutePendingPopulatesDataplaneFields(t *testing.T) {
	store := NewStore(time.Minute)

	store.EnsureRoutePending(
		"game.example.com",
		"192.0.2.20",
		"udp",
		30000,
		31000,
	)

	leases := store.Poll("edge-1", 0, 1)
	if len(leases) != 1 {
		t.Fatalf("expected one route lease, got %d", len(leases))
	}

	lease := leases[0]
	if lease.Type != TypeRoute {
		t.Fatalf("expected route lease, got %q", lease.Type)
	}
	if lease.DestinationIP != "192.0.2.20" {
		t.Fatalf("unexpected destination IP %q", lease.DestinationIP)
	}
	if lease.Protocol != "udp" {
		t.Fatalf("unexpected protocol %q", lease.Protocol)
	}
	if lease.DestinationPortStart != 30000 ||
		lease.DestinationPortEnd != 31000 {
		t.Fatalf(
			"unexpected port range %d-%d",
			lease.DestinationPortStart,
			lease.DestinationPortEnd,
		)
	}
}
