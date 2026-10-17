package entitlement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	key     string
	http    *http.Client
}

func NewClient(baseURL, key string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		key:     strings.TrimSpace(key),
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) Fetch(ctx context.Context) (Snapshot, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.baseURL+"/api/v1/internal/network/entitlements",
		nil,
	)
	if err != nil {
		return Snapshot{}, fmt.Errorf("create entitlement snapshot request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Network-Control-Key", c.key)

	resp, err := c.http.Do(req)
	if err != nil {
		return Snapshot{}, fmt.Errorf("fetch entitlement snapshot: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf(
			"entitlement snapshot returned HTTP %d",
			resp.StatusCode,
		)
	}

	var snapshot Snapshot
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode entitlement snapshot: %w", err)
	}

	return snapshot, nil
}
