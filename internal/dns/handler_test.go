package dns

import (
 "testing"
 "time"

 "github.com/wraplink/dns-control/internal/agentregistry"
 "github.com/wraplink/dns-control/internal/allocation"
)

func TestHealthyAgentFailsClosed(t *testing.T) {
 h := &Handler{agentMaxAge: 30 * time.Second}
 if h.healthyAgent("edge-01", allocation.TypeSNI) {
  t.Fatal("missing agent registry must fail closed")
 }
 h.agents = agentregistry.NewStore()
 if h.healthyAgent("edge-01", allocation.TypeSNI) {
  t.Fatal("unknown agent must fail closed")
 }
 h.agents.Upsert("edge-01", "eu-1", agentregistry.Capacity{SNI: 10, Route: 0})
 if !h.healthyAgent("edge-01", allocation.TypeSNI) {
  t.Fatal("fresh online SNI agent should be eligible")
 }
 if h.healthyAgent("edge-01", allocation.TypeRoute) {
  t.Fatal("agent with no route capacity must not serve route answers")
 }
 h.agentMaxAge = -time.Second
 if h.healthyAgent("edge-01", allocation.TypeSNI) {
  t.Fatal("stale agent must not be selected before offline sweeper")
 }
 h.agentMaxAge = 30 * time.Second
 h.agents.MarkOfflineBefore(time.Now().Add(time.Second))
 if h.healthyAgent("edge-01", allocation.TypeSNI) {
  t.Fatal("offline agent must not be selected")
 }
}
