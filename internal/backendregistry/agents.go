package backendregistry

import (
 "context"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "strings"

 "github.com/wraplink/dns-control/internal/agentregistry"
)

// AgentSnapshot is the Backend-owned inventory contract. A revision is
// mandatory to prevent older responses from replacing newer health state.
type AgentSnapshot struct {
 Revision uint64 `json:"revision"`
 Agents []agentregistry.Agent `json:"agents"`
}

// FetchAgents reads authoritative agent health without mutating DNS state.
func (c *Client) FetchAgents(ctx context.Context) (AgentSnapshot, error) {
 req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/internal/network/agents/snapshot", nil)
 if err != nil { return AgentSnapshot{}, fmt.Errorf("create agent snapshot request: %w", err) }
 req.Header.Set("Accept", "application/json")
 req.Header.Set("X-Network-Control-Key", c.controlKey)
 resp, err := c.httpClient.Do(req)
 if err != nil { return AgentSnapshot{}, fmt.Errorf("fetch agent snapshot: %w", err) }
 defer resp.Body.Close()
 if resp.StatusCode != http.StatusOK {
  body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
  return AgentSnapshot{}, fmt.Errorf("agent snapshot backend status %d: %s", resp.StatusCode, string(body))
 }
 var snapshot AgentSnapshot
 if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&snapshot); err != nil {
  return AgentSnapshot{}, fmt.Errorf("decode agent snapshot: %w", err)
 }
 if snapshot.Revision == 0 || snapshot.Agents == nil {
  return AgentSnapshot{}, fmt.Errorf("invalid agent snapshot: missing revision or agents")
 }
 seen := make(map[string]struct{}, len(snapshot.Agents))
 for _, agent := range snapshot.Agents {
  id := strings.TrimSpace(agent.ID)
  if id == "" || agent.LastSeenAt.IsZero() || (agent.Status != agentregistry.StatusOnline && agent.Status != agentregistry.StatusOffline) {
   return AgentSnapshot{}, fmt.Errorf("invalid agent snapshot: malformed agent record")
  }
  if _, exists := seen[id]; exists { return AgentSnapshot{}, fmt.Errorf("invalid agent snapshot: duplicate agent ID") }
  seen[id] = struct{}{}
 }
 return snapshot, nil
}
