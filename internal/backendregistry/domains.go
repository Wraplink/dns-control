package backendregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/wraplink/dns-control/internal/domain"
)

type DomainSnapshot struct {
	Revision uint64 `json:"revision"`
	Domains []domain.Rule `json:"domains"`
}

func (c *Client) FetchDomains(ctx context.Context) (DomainSnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/internal/network/domains", nil)
	if err != nil { return DomainSnapshot{}, err }
	req.Header.Set("X-Network-Control-Key", c.controlKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil { return DomainSnapshot{}, err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return DomainSnapshot{}, fmt.Errorf("domain snapshot returned HTTP %d", resp.StatusCode) }
	var snapshot DomainSnapshot
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&snapshot); err != nil { return DomainSnapshot{}, fmt.Errorf("decode domain snapshot: %w", err) }
	if snapshot.Domains == nil { return DomainSnapshot{}, fmt.Errorf("domain snapshot missing domains") }
	return snapshot, nil
}
