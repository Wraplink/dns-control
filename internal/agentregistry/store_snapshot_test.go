package agentregistry

import (
	"testing"
	"time"
)

func TestReplaceSnapshotPreservesHealthAndRemovesMissing(t *testing.T) {
	store := NewStore()
	store.Upsert("old", "eu-1", Capacity{SNI: 1})
	lastSeen := time.Now().UTC().Add(-time.Minute)
	snapshot := []Agent{{ID: "new", Status: StatusOffline, LastSeenAt: lastSeen, Capacity: Capacity{SNI: 2}, Diagnostics: Diagnostics{AdvertisedIPs: []string{"192.0.2.1"}}}}
	store.ReplaceSnapshot(snapshot)
	if _, exists := store.Get("old"); exists {
		t.Fatal("agent absent from authoritative snapshot must be removed")
	}
	agent, exists := store.Get("new")
	if !exists || agent.Status != StatusOffline || !agent.LastSeenAt.Equal(lastSeen) {
		t.Fatal("snapshot must preserve authoritative offline status and heartbeat")
	}
	snapshot[0].Diagnostics.AdvertisedIPs[0] = "192.0.2.2"
	agent, _ = store.Get("new")
	if agent.Diagnostics.AdvertisedIPs[0] != "192.0.2.1" {
		t.Fatal("snapshot must be copied, not aliased")
	}
}

func TestReplaceVersionedSnapshotRejectsOutOfOrder(t *testing.T) {
	store := NewStore()
	fresh := []Agent{{ID: "new", Status: StatusOnline, LastSeenAt: time.Now().UTC(), Capacity: Capacity{SNI: 2}}}
	if !store.ReplaceVersionedSnapshot(10, fresh) {
		t.Fatal("newer revision should apply")
	}
	if store.ReplaceVersionedSnapshot(9, []Agent{{ID: "stale", Status: StatusOnline}}) {
		t.Fatal("older revision must be rejected")
	}
	if store.ReplaceVersionedSnapshot(10, nil) {
		t.Fatal("duplicate revision must be rejected")
	}
	if _, ok := store.Get("new"); !ok {
		t.Fatal("newer agent inventory was lost")
	}
	if _, ok := store.Get("stale"); ok {
		t.Fatal("stale snapshot overwrote newer inventory")
	}
}

func TestRegistryReadsDoNotExposeMutableSnapshotSlices(t *testing.T) {
	store := NewStore()
	store.ReplaceVersionedSnapshot(1, []Agent{{ID: "edge", Status: StatusOnline, LastSeenAt: time.Now(), Diagnostics: Diagnostics{AdvertisedIPs: []string{"192.0.2.10"}, UpstreamDNS: []string{"1.1.1.1"}}}})
	agent, ok := store.Get("edge")
	if !ok {
		t.Fatal("missing agent")
	}
	agent.Diagnostics.AdvertisedIPs[0] = "192.0.2.20"
	listed := store.List()
	listed[0].Diagnostics.UpstreamDNS[0] = "9.9.9.9"
	again, _ := store.Get("edge")
	if again.Diagnostics.AdvertisedIPs[0] != "192.0.2.10" || again.Diagnostics.UpstreamDNS[0] != "1.1.1.1" {
		t.Fatal("registry health data mutated through read result")
	}
}
