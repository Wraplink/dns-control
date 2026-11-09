package backendregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/wraplink/dns-control/internal/agentregistry"
)

// AgentSnapshot is an atomic Backend-owned inventory. Backend may paginate
// using next_cursor, but all pages must share one immutable revision.
type AgentSnapshot struct {
	Revision   uint64                `json:"revision"`
	Agents     []agentregistry.Agent `json:"agents"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

// FetchAgents loads every page before returning any inventory to DNS.
// Failed or inconsistent pagination never results in a partial replacement.
func (c *Client) FetchAgents(ctx context.Context) (AgentSnapshot, error) {
	var result AgentSnapshot
	seenIDs := make(map[string]struct{})
	seenCursors := make(map[string]struct{})
	cursor := ""
	for {
		endpoint := c.baseURL + "/api/v1/internal/network/agents/snapshot"
		if cursor != "" {
			endpoint += "?cursor=" + url.QueryEscape(cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return AgentSnapshot{}, fmt.Errorf("create agent snapshot request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Network-Control-Key", c.controlKey)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return AgentSnapshot{}, fmt.Errorf("fetch agent snapshot: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			return AgentSnapshot{}, fmt.Errorf("agent snapshot backend status %d: %s", resp.StatusCode, string(body))
		}
		var page AgentSnapshot
		err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return AgentSnapshot{}, fmt.Errorf("decode agent snapshot page: %w", err)
		}
		if page.Revision == 0 || page.Agents == nil {
			return AgentSnapshot{}, fmt.Errorf("invalid agent snapshot: missing revision or agents")
		}
		if result.Revision == 0 {
			result.Revision = page.Revision
			result.Agents = make([]agentregistry.Agent, 0)
		}
		if result.Revision != page.Revision {
			return AgentSnapshot{}, fmt.Errorf("agent snapshot revision changed during pagination")
		}
		for _, agent := range page.Agents {
			id := strings.TrimSpace(agent.ID)
			if id == "" || agent.LastSeenAt.IsZero() || (agent.Status != agentregistry.StatusOnline && agent.Status != agentregistry.StatusOffline) {
				return AgentSnapshot{}, fmt.Errorf("invalid agent snapshot: malformed agent record")
			}
			if _, exists := seenIDs[id]; exists {
				return AgentSnapshot{}, fmt.Errorf("invalid agent snapshot: duplicate agent ID")
			}
			seenIDs[id] = struct{}{}
			result.Agents = append(result.Agents, agent)
		}
		if page.NextCursor == "" {
			return result, nil
		}
		if _, exists := seenCursors[page.NextCursor]; exists {
			return AgentSnapshot{}, fmt.Errorf("agent snapshot pagination cursor cycle")
		}
		seenCursors[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
}
