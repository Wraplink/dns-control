package dns

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	mdns "github.com/miekg/dns"
)

type Resolver struct {
	client      *mdns.Client
	upstreams   []string
	parentURL   string
	parentToken string
	httpClient  *http.Client
}

func NewResolver(upstreams []string) *Resolver {
	return &Resolver{
		client: &mdns.Client{
			Timeout: 5 * time.Second,
		},
		upstreams:  append([]string(nil), upstreams...),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// SetParent configures an authenticated upstream DNS Control over HTTPS.
// Parent resolution is used only when all local DNS upstreams fail.
func (r *Resolver) SetParent(url, token string) {
	r.parentURL = strings.TrimRight(strings.TrimSpace(url), "/")
	r.parentToken = token
}

func (r *Resolver) resolveParent(req *mdns.Msg) (*mdns.Msg, error) {
	wire, err := req.Pack()
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodPost, r.parentURL+"/api/v1/dns/resolve", bytes.NewReader(wire))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/dns-message")
	request.Header.Set("Authorization", "Bearer "+r.parentToken)
	response, err := r.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("parent resolver returned HTTP %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 65536))
	if err != nil {
		return nil, err
	}
	var result mdns.Msg
	if err := result.Unpack(payload); err != nil {
		return nil, err
	}
	if result.Id != req.Id || !result.Response {
		return nil, fmt.Errorf("parent DNS response does not match request")
	}
	return &result, nil
}

func (r *Resolver) Resolve(req *mdns.Msg) (*mdns.Msg, error) {
	if req == nil {
		return nil, fmt.Errorf("nil DNS request")
	}

	if len(req.Question) == 0 {
		return nil, fmt.Errorf("DNS request has no questions")
	}

	if len(r.upstreams) == 0 && r.parentURL == "" {
		return nil, fmt.Errorf("no upstream DNS servers configured")
	}

	var lastErr error

	for _, upstream := range r.upstreams {
		resp, _, err := r.client.Exchange(req, upstream)

		if err == nil {
			return resp, nil
		}

		lastErr = err

		log.Printf(
			"dns: upstream %s failed: %v",
			upstream,
			err,
		)
	}

	if r.parentURL != "" {
		response, err := r.resolveParent(req)
		if err == nil {
			return response, nil
		}
		return nil, fmt.Errorf("parent DNS resolution failed: %w", err)
	}
	return nil, fmt.Errorf("all upstream DNS servers failed: %w", lastErr)
}
