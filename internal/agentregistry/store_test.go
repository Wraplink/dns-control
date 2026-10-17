package agentregistry

import (
	"testing"
	"time"
)

func TestAgentLifecycle(t *testing.T) {
	store := NewStore()

	agent := store.Upsert("edge-1", "eu-1", Capacity{SNI: 10, Route: 5})
	if agent.Status != StatusOnline {
		t.Fatalf("expected online agent, got %q", agent.Status)
	}
	if agent.Region != "eu-1" || agent.Capacity.SNI != 10 || agent.Capacity.Route != 5 {
		t.Fatalf("unexpected agent data: %#v", agent)
	}

	if changed := store.MarkOfflineBefore(time.Now().UTC().Add(time.Second)); changed != 1 {
		t.Fatalf("expected one agent to become offline, got %d", changed)
	}

	agent, ok := store.Touch("edge-1")
	if !ok {
		t.Fatal("expected registered agent")
	}
	if agent.Status != StatusOnline {
		t.Fatalf("expected touch to restore online state, got %q", agent.Status)
	}
}
