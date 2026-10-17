package agentregistry

import "testing"

func TestRegisterAndHeartbeat(t *testing.T) {
	store := NewStore()
	agent := store.Register("edge-eu-1", 10, 20)
	if agent.ID != "edge-eu-1" || agent.SNICapacity != 10 || agent.RouteCapacity != 20 {
		t.Fatalf("unexpected agent: %+v", agent)
	}
	if agent.Status != StatusOnline {
		t.Fatalf("expected online status, got %s", agent.Status)
	}

	updated := store.Register("edge-eu-1", 5, 7)
	if updated.SNICapacity != 5 || updated.RouteCapacity != 7 {
		t.Fatalf("capacity was not updated: %+v", updated)
	}
	if !store.Heartbeat("edge-eu-1") {
		t.Fatal("expected heartbeat to succeed")
	}
	if store.Heartbeat("missing") {
		t.Fatal("unknown agent heartbeat must fail")
	}
}
