package entitlement

import (
	"net"
	"testing"
	"time"
)

func TestAllowedUsesFreshUsableSnapshot(t *testing.T) {
	store := NewStore(time.Minute)
	store.Replace(Snapshot{
		Entitlements: []Item{
			{
				ClientIP:       "203.0.113.10",
				RemainingBytes: 1024,
				ExpiresAt:      time.Now().UTC().Add(time.Hour),
			},
		},
	})

	if !store.Allowed(net.ParseIP("203.0.113.10")) {
		t.Fatal("expected entitled IPv4 to be allowed")
	}
	if store.Allowed(net.ParseIP("203.0.113.11")) {
		t.Fatal("unexpected allowance for unknown IPv4")
	}
}

func TestReplaceDropsExpiredOrExhaustedItems(t *testing.T) {
	store := NewStore(time.Minute)
	store.Replace(Snapshot{
		Entitlements: []Item{
			{
				ClientIP:       "203.0.113.10",
				RemainingBytes: 0,
				ExpiresAt:      time.Now().UTC().Add(time.Hour),
			},
			{
				ClientIP:       "203.0.113.11",
				RemainingBytes: 1024,
				ExpiresAt:      time.Now().UTC().Add(-time.Second),
			},
		},
	})

	if store.Count() != 0 {
		t.Fatalf("expected unusable entitlements to be discarded, got %d", store.Count())
	}
}

func TestAllowedFailsClosedAfterSnapshotBecomesStale(t *testing.T) {
	store := NewStore(15 * time.Millisecond)
	store.Replace(Snapshot{
		Entitlements: []Item{
			{
				ClientIP:       "203.0.113.10",
				RemainingBytes: 1024,
				ExpiresAt:      time.Now().UTC().Add(time.Hour),
			},
		},
	})

	time.Sleep(25 * time.Millisecond)

	if store.Allowed(net.ParseIP("203.0.113.10")) {
		t.Fatal("stale snapshot must fail closed")
	}
}

func TestReplaceCanonicalizesIPv4(t *testing.T) {
	store := NewStore(time.Minute)
	store.Replace(Snapshot{
		Entitlements: []Item{
			{
				ClientIP:       "203.0.113.010",
				RemainingBytes: 1024,
				ExpiresAt:      time.Now().UTC().Add(time.Hour),
			},
		},
	})

	// Go deliberately rejects ambiguous dotted-decimal forms with leading
	// zeroes, so the invalid address must not enter the entitlement cache.
	if store.Count() != 0 {
		t.Fatal("invalid IPv4 should not be cached")
	}
}
