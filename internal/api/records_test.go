package api

import (
	"encoding/json"
	"net"
	"net/http"
	"time"

	mdns "github.com/miekg/dns"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wraplink/dns-control/internal/dns"
)

func TestResolveRecordsValidation(t *testing.T) {
	h := &Handler{}
	tests := []struct {
		name   string
		method string
		body   string
		want   int
	}{
		{"wrong method", http.MethodGet, "", http.StatusMethodNotAllowed},
		{"no resolver", http.MethodPost, `{"domain":"example.com"}`, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/v1/dns/resolve-records", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.resolveRecords(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestResolveRecordsBadInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"invalid JSON", "{"},
		{"empty domain", `{"domain":""}`},
		{"invalid domain", `{"domain":"invalid..example"}`},
		{"unsupported type", `{"domain":"example.com","types":["AXFR"]}`},
		{"too many types", `{"domain":"example.com","types":["A","A","A","A","A","A","A","A","A","A","A"]}`},
	}
	h := &Handler{resolver: dns.NewResolver([]string{"127.0.0.1:53"})}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/dns/resolve-records", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.resolveRecords(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestResolveRecordsMultipleAddresses(t *testing.T) {
	// A local deterministic DNS server exercises the real resolver without
	// requiring public DNS or the production Edge Agent.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	server := &mdns.Server{PacketConn: pc, Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
		m := new(mdns.Msg)
		m.SetReply(r)
		if len(r.Question) != 1 {
			_ = w.WriteMsg(m)
			return
		}
		switch r.Question[0].Qtype {
		case mdns.TypeA:
			for _, ip := range []string{"192.0.2.10", "192.0.2.11"} {
				rr, _ := mdns.NewRR("example.com. 120 IN A " + ip)
				m.Answer = append(m.Answer, rr)
			}
		case mdns.TypeAAAA:
			rr, _ := mdns.NewRR("example.com. 180 IN AAAA 2001:db8::10")
			m.Answer = append(m.Answer, rr)
		}
		_ = w.WriteMsg(m)
	})}
	go func() { _ = server.ActivateAndServe() }()
	defer server.Shutdown()
	// Wait for the local server's handler to accept requests.
	time.Sleep(20 * time.Millisecond)

	h := &Handler{resolver: dns.NewResolver([]string{pc.LocalAddr().String()})}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dns/resolve-records", strings.NewReader(`{"domain":"example.com"}`))
	rec := httptest.NewRecorder()
	h.resolveRecords(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
	}
	var got recordsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Rcode != "NOERROR" || len(got.Answers) != 3 {
		t.Fatalf("unexpected response: %+v", got)
	}
	if got.Answers[0].Type != "A" || got.Answers[0].TTL != 120 || got.Answers[1].Value != "192.0.2.11" || got.Answers[2].Type != "AAAA" || got.Answers[2].TTL != 180 {
		t.Fatalf("unexpected records: %+v", got.Answers)
	}
}

func TestResolveRecordsNXDOMAIN(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	server := &mdns.Server{PacketConn: pc, Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
		m := new(mdns.Msg)
		m.SetRcode(r, mdns.RcodeNameError)
		_ = w.WriteMsg(m)
	})}
	go func() { _ = server.ActivateAndServe() }()
	defer server.Shutdown()
	time.Sleep(20 * time.Millisecond)
	h := &Handler{resolver: dns.NewResolver([]string{pc.LocalAddr().String()})}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dns/resolve-records", strings.NewReader(`{"domain":"missing.example","types":["A"]}`))
	rec := httptest.NewRecorder()
	h.resolveRecords(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
	}
	var got recordsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Rcode != "NXDOMAIN" || len(got.Answers) != 0 {
		t.Fatalf("unexpected negative response: %+v", got)
	}
}

func TestResolveRecordsOriginFailure(t *testing.T) {
	// A closed loopback UDP port gives a deterministic transport error.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := pc.LocalAddr().String()
	_ = pc.Close()
	h := &Handler{resolver: dns.NewResolver([]string{address})}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dns/resolve-records", strings.NewReader(`{"domain":"example.com","types":["A"]}`))
	rec := httptest.NewRecorder()
	h.resolveRecords(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502: %s", rec.Code, rec.Body.String())
	}
}
