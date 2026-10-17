package backendregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	controlKey string
	httpClient *http.Client
}

type RegisterRequest struct {
	ID        string   `json:"id"`
	Region    string   `json:"region,omitempty"`
	Addresses []string `json:"addresses"`
}

func NewClient(baseURL, controlKey string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		controlKey: strings.TrimSpace(controlKey),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) Register(ctx context.Context, request RegisterRequest) error {
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal dns-control registration: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/api/v1/internal/network/dns-controls/register",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create dns-control registration request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("X-Network-Control-Key", c.controlKey)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("register dns-control: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}

	payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	message := strings.TrimSpace(string(payload))
	if message == "" {
		message = response.Status
	}
	return fmt.Errorf("register dns-control: backend returned %s: %s", response.Status, message)
}
