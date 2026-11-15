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
				DataLimitBytes: 1024,
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

func TestConnectionModesStoredBySourceIP(t *testing.T) {
	store := NewStore(time.Minute)
	store.Replace(Snapshot{
		Entitlements: []Item{
			{ClientIP: "203.0.113.10", ConnectionMode: "gaming", RemainingBytes: 100, ExpiresAt: time.Now().Add(time.Hour)},
			{ClientIP: "203.0.113.11", ConnectionMode: "streaming", RemainingBytes: 100, ExpiresAt: time.Now().Add(time.Hour)},
			{ClientIP: "203.0.113.12", RemainingBytes: 100, ExpiresAt: time.Now().Add(time.Hour)},
			{ClientIP: "203.0.113.13", ConnectionMode: "unknown", RemainingBytes: 100, ExpiresAt: time.Now().Add(time.Hour)},
		},
	})
	for ip, expectedMode := range map[string]string{
		"203.0.113.10": "gaming",
		"203.0.113.11": "streaming",
		"203.0.113.12": "smart",
	} {
		got, ok := store.Get(net.ParseIP(ip))
		if !ok || got.ConnectionMode != expectedMode {
			t.Errorf("IP %s: got mode %q allowed=%t; want %q", ip, got.ConnectionMode, ok, expectedMode)
		}
	}
	if store.Allowed(net.ParseIP("203.0.113.13")) {
		t.Fatal("unknown mode must not silently enable managed DNS policy")
	}
}

func TestUnlimitedEntitlementRemainsAuthorized(t *testing.T) {
	store := NewStore(time.Minute)
	store.Replace(Snapshot{Entitlements: []Item{{
		EntitlementID:  "unlimited-1",
		SubscriptionID: "subscription-1",
		ClientIP:       "203.0.113.50",
		DataLimitBytes: 0,
		RemainingBytes: 0,
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
	}}})
	if !store.Allowed(net.ParseIP("203.0.113.50")) {
		t.Fatal("unlimited entitlement should be authorized")
	}
	_, grants := store.AccessSnapshot()
	if len(grants) != 1 || grants[0].ClientIP != "203.0.113.50" {
		t.Fatalf("unlimited entitlement missing from access grants: %+v", grants)
	}
}

func TestAccessSnapshotTracksIPOwnershipHandover(t *testing.T) {
	store := NewStore(time.Minute)
	expiresAt := time.Now().UTC().Add(time.Hour)
	ip := "203.0.113.60"

	store.Replace(Snapshot{Entitlements: []Item{{
		EntitlementID:  "old-entitlement",
		SubscriptionID: "old-subscription",
		ClientIP:       ip,
		ExpiresAt:      expiresAt,
	}}})
	firstRevision, firstGrants := store.AccessSnapshot()
	if len(firstGrants) != 1 || firstGrants[0].EntitlementID != "old-entitlement" {
		t.Fatalf("expected old owner's grant, got %+v", firstGrants)
	}

	store.Replace(Snapshot{Entitlements: []Item{}})
	revokedRevision, revokedGrants := store.AccessSnapshot()
	if revokedRevision != firstRevision+1 || len(revokedGrants) != 0 {
		t.Fatalf("expected immediate empty grant set after revocation, revision=%d grants=%+v", revokedRevision, revokedGrants)
	}

	store.Replace(Snapshot{Entitlements: []Item{{
		EntitlementID:  "new-entitlement",
		SubscriptionID: "new-subscription",
		ClientIP:       ip,
		ExpiresAt:      expiresAt,
	}}})
	newRevision, newGrants := store.AccessSnapshot()
	if newRevision != revokedRevision+1 || len(newGrants) != 1 || newGrants[0].EntitlementID != "new-entitlement" {
		t.Fatalf("expected new owner only, revision=%d grants=%+v", newRevision, newGrants)
	}
}

func TestConflictingIPOwnersFailClosed(t *testing.T) {
	store := NewStore(time.Minute)
	expiresAt := time.Now().UTC().Add(time.Hour)
	store.Replace(Snapshot{Entitlements: []Item{
		{EntitlementID: "old", UserID: "user-1", SubscriptionID: "sub-1", ClientIP: "203.0.113.90", ExpiresAt: expiresAt},
		{EntitlementID: "new", UserID: "user-2", SubscriptionID: "sub-2", ClientIP: "203.0.113.90", ExpiresAt: expiresAt},
	}})
	if store.Allowed(net.ParseIP("203.0.113.90")) {
		t.Fatal("conflicting IP owners must never be authorized")
	}
	_, grants := store.AccessSnapshot()
	if len(grants) != 0 {
		t.Fatalf("conflicting IP owners must not reach edge agents: %+v", grants)
	}
}

func TestInvalidNegativeBandwidthLimitFailsClosed(t *testing.T) {
	store := NewStore(time.Minute)
	store.Replace(Snapshot{Entitlements: []Item{{
		EntitlementID: "invalid", ClientIP: "203.0.113.91",
		DataLimitBytes: -1, RemainingBytes: 100,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}}})
	if store.Allowed(net.ParseIP("203.0.113.91")) {
		t.Fatal("negative data limit must not authorize a client")
	}
	_, grants := store.AccessSnapshot()
	if len(grants) != 0 {
		t.Fatalf("invalid bandwidth limit leaked into access grants: %+v", grants)
	}
}
