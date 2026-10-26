package backendregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrStaleAssignment means the backend no longer recognizes this generation.
var ErrStaleAssignment = errors.New("stale route assignment")

type RouteActivation struct {
	Domain     string `json:"domain"`
	AgentID    string `json:"agent_id"`
	PublicIP   string `json:"public_ip"`
	Generation int64  `json:"generation"`
	Active     bool   `json:"active"`
}

func (c *Client) ReportRouteActivation(ctx context.Context, report RouteActivation) error {
	payload, err := json.Marshal(report)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v1/internal/network/route-assignments/report", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("X-Network-Control-Key", c.controlKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("report route activation: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusConflict {
			return ErrStaleAssignment
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("report route activation: HTTP %d: %s", resp.StatusCode, body)
	}
	return nil
}
