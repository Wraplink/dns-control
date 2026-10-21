package dns

import (
 "testing"
 "time"

 "github.com/wraplink/dns-control/internal/agentregistry"
 "github.com/wraplink/dns-control/internal/allocation"
)

func TestHealthyAgentRequiresRegistrationAndCapacity(t *testing.T) {
 agents := agentregistry.NewStore()
 h := &Handler{agents: agents}
 if h.healthyAgent("missing", allocation.TypeSNI) { t.Fatal("unregistered agent must not serve DNS") }
 agents.Upsert("sni-agent", "eu", agentregistry.Capacity{SNI: 2})
 if !h.healthyAgent("sni-agent", allocation.TypeSNI) { t.Fatal("SNI agent should be eligible") }
 if h.healthyAgent("sni-agent", allocation.TypeRoute) { t.Fatal("SNI-only agent must not serve routes") }
 agents.Upsert("route-agent", "eu", agentregistry.Capacity{Route: 1})
 if !h.healthyAgent("route-agent", allocation.TypeRoute) { t.Fatal("route agent should be eligible") }
 if h.healthyAgent("route-agent", allocation.TypeSNI) { t.Fatal("route-only agent must not serve SNI") }
 agents.MarkOfflineBefore(time.Now().UTC().Add(time.Second))
 if h.healthyAgent("sni-agent", allocation.TypeSNI) { t.Fatal("offline agent must not serve SNI") }
}
