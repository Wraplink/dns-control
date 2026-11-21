package dns

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mdns "github.com/miekg/dns"
)

func TestClientResolutionUsesConfiguredAgent(t *testing.T) {
	const token = "test-agent-token"
	calls := 0
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.URL.Path != "/api/v1/dns/resolve" || req.Method != http.MethodPost {
			t.Errorf("unexpected agent request: %s %s", req.Method, req.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if req.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("missing agent authorization")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var question mdns.Msg
		payload, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read DNS request: %v", err)
			return
		}
		if err := question.Unpack(payload); err != nil {
			t.Errorf("unpack DNS request: %v", err)
			http.Error(w, "invalid DNS message", http.StatusBadRequest)
			return
		}
		reply := new(mdns.Msg)
		reply.SetReply(&question)
		reply.Answer = []mdns.RR{&mdns.A{
			Hdr: mdns.RR_Header{Name: question.Question[0].Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 123},
			A:   []byte{192, 0, 2, 42},
		}}
		wire, err := reply.Pack()
		if err != nil {
			t.Errorf("pack reply: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(wire)
	}))
	defer agent.Close()

	resolver := NewResolver([]string{"127.0.0.1:1"})
	resolver.SetOriginAgent(agent.URL, token)
	query := new(mdns.Msg)
	query.SetQuestion("unmanaged.example.", mdns.TypeA)
	answer, err := resolver.ResolveContext(context.Background(), query)
	if err != nil {
		t.Fatalf("client resolution: %v", err)
	}
	if calls != 1 || len(answer.Answer) != 1 || answer.Answer[0].Header().Ttl != 123 {
		t.Fatalf("unexpected agent resolution: calls=%d answers=%v", calls, answer.Answer)
	}
}

func TestClientResolutionDoesNotFallbackWhenAgentFails(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "agent unavailable", http.StatusServiceUnavailable)
	}))
	defer agent.Close()
	resolver := NewResolver([]string{"127.0.0.1:1"})
	resolver.SetOriginAgent(agent.URL, "token")
	query := new(mdns.Msg)
	query.SetQuestion("unmanaged.example.", mdns.TypeAAAA)
	_, err := resolver.ResolveContext(context.Background(), query)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("expected agent failure without local fallback, got %v", err)
	}
}
