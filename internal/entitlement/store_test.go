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

func TestAccessSnapshotAdvancesRevisionWhenMembershipChanges(t *testing.T) {
	store := NewStore(time.Minute)
	expiresAt := time.Now().UTC().Add(time.Hour)

	store.Replace(Snapshot{
		Entitlements: []Item{
			{
				EntitlementID:  "ent-1",
				SubscriptionID: "sub-1",
				ClientIP:       "203.0.113.10",
				RemainingBytes: 1024,
				ExpiresAt:      expiresAt,
			},
		},
	})

	revision, grants := store.AccessSnapshot()
	if revision != 1 || len(grants) != 1 {
		t.Fatalf("expected revision=1 grants=1, got revision=%d grants=%d", revision, len(grants))
	}

	store.Replace(Snapshot{
		Entitlements: []Item{
			{
				EntitlementID:  "ent-1",
				SubscriptionID: "sub-1",
				ClientIP:       "203.0.113.10",
				RemainingBytes: 512,
				ExpiresAt:      expiresAt,
			},
		},
	})

	unchangedRevision, _ := store.AccessSnapshot()
	if unchangedRevision != revision {
		t.Fatalf("usage-only changes should not advance access revision: %d -> %d", revision, unchangedRevision)
	}

	store.Replace(Snapshot{Entitlements: []Item{}})
	removedRevision, removed := store.AccessSnapshot()
	if removedRevision != revision+1 || len(removed) != 0 {
		t.Fatalf("expected removal revision=%d empty grants, got revision=%d grants=%d", revision+1, removedRevision, len(removed))
	}
}

func TestAccessSnapshotRevokesOnceWhenBackendSnapshotBecomesStale(t *testing.T) {
	store := NewStore(15 * time.Millisecond)
	store.Replace(Snapshot{
		Entitlements: []Item{
			{
				EntitlementID:  "ent-1",
				SubscriptionID: "sub-1",
				ClientIP:       "203.0.113.10",
				RemainingBytes: 1024,
				ExpiresAt:      time.Now().UTC().Add(time.Hour),
			},
		},
	})

	initialRevision, _ := store.AccessSnapshot()
	time.Sleep(25 * time.Millisecond)

	staleRevision, grants := store.AccessSnapshot()
	if staleRevision != initialRevision+1 || len(grants) != 0 {
		t.Fatalf("expected stale revocation revision=%d, got revision=%d grants=%d", initialRevision+1, staleRevision, len(grants))
	}

	secondRevision, secondGrants := store.AccessSnapshot()
	if secondRevision != staleRevision || len(secondGrants) != 0 {
		t.Fatalf("stale state must not bump every call: revision=%d grants=%d", secondRevision, len(secondGrants))
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
