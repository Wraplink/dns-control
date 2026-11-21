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

func TestOriginAgentResolution(t *testing.T) {
	token := strings.Repeat("x", 32)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/dns/resolve" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("unexpected origin request %s", r.URL.Path)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var q mdns.Msg
		payload, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil || len(payload) > 4096 {
			http.Error(w, "bad request size", 400)
			return
		}
		if err := q.Unpack(payload); err != nil {
			t.Errorf("decode origin DNS request: %v (size=%d)", err, len(payload))
			http.Error(w, "bad request", 400)
			return
		}
		reply := new(mdns.Msg)
		reply.SetRcode(&q, mdns.RcodeNameError)
		data, _ := reply.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(data)
	}))
	defer server.Close()
	resolver := NewResolver([]string{"127.0.0.1:1"})
	resolver.httpClient = server.Client()
	resolver.SetOriginAgent(server.URL, token)
	q := new(mdns.Msg)
	q.SetQuestion("nonexistent.example.", mdns.TypeA)
	got, err := resolver.ResolveOrigin(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rcode != mdns.RcodeNameError {
		t.Fatalf("expected NXDOMAIN, got %d", got.Rcode)
	}
}

func TestOriginAgentRejectsMismatchedResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := new(mdns.Msg)
		q.SetQuestion("wrong.example.", mdns.TypeA)
		reply := new(mdns.Msg)
		reply.SetReply(q)
		data, _ := reply.Pack()
		_, _ = w.Write(data)
	}))
	defer server.Close()
	resolver := NewResolver(nil)
	resolver.httpClient = server.Client()
	resolver.SetOriginAgent(server.URL, strings.Repeat("x", 32))
	q := new(mdns.Msg)
	q.SetQuestion("real.example.", mdns.TypeA)
	if _, err := resolver.ResolveOrigin(context.Background(), q); err == nil {
		t.Fatal("expected mismatched response to be rejected")
	}
}
