package dns

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mdns "github.com/miekg/dns"
)

func TestParentHopLimitRejectsCircularResolution(t *testing.T) {
	calls := 0
	parent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "should not be called", http.StatusBadGateway)
	}))
	defer parent.Close()
	resolver := NewResolver(nil)
	resolver.SetParent(parent.URL, strings.Repeat("a", 32))
	query := new(mdns.Msg)
	query.SetQuestion("example.org.", mdns.TypeA)
	_, err := resolver.ResolveContext(WithParentHopCount(context.Background(), MaxParentHops), query)
	if err == nil || !strings.Contains(err.Error(), "maximum hop count") {
		t.Fatalf("expected hop-limit error, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("parent called after hop limit: %d", calls)
	}
}

func TestParentResolutionPropagatesHopHeader(t *testing.T) {
	parent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Wraplink-DNS-Hop") != "2" {
			t.Errorf("unexpected hop header %q", r.Header.Get("X-Wraplink-DNS-Hop"))
		}
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("b", 32) {
			t.Error("missing parent authentication")
		}
		payload := make([]byte, 65536)
		n, _ := r.Body.Read(payload)
		var query mdns.Msg
		if err := query.Unpack(payload[:n]); err != nil {
			t.Error(err)
			return
		}
		answer := new(mdns.Msg)
		answer.SetReply(&query)
		wire, _ := answer.Pack()
		_, _ = w.Write(wire)
	}))
	defer parent.Close()
	resolver := NewResolver(nil)
	resolver.SetParent(parent.URL, strings.Repeat("b", 32))
	query := new(mdns.Msg)
	query.SetQuestion("example.org.", mdns.TypeA)
	response, err := resolver.ResolveContext(WithParentHopCount(context.Background(), 1), query)
	if err != nil || response == nil || !response.Response {
		t.Fatalf("unexpected parent response: %v, %v", response, err)
	}
}
