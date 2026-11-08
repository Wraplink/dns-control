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
