package dns

import (
	"testing"

	mdns "github.com/miekg/dns"

	"github.com/wraplink/dns-control/internal/allocation"
	"github.com/wraplink/dns-control/internal/domain"
)

func TestHijackUsesParentDomainAllocationForSubdomains(t *testing.T) {
	rule := domain.Rule{}
	rule.Domain = "playstation.com"
	rule.Mode = domain.ModeHijack
	rule.ConnectionModes = []string{"gaming"}
	rules, err := domain.NewStore([]domain.Rule{rule})
	if err != nil {
		t.Fatal(err)
	}

	allocations := allocation.NewStore()
	allocations.EnsurePending("playstation.com", allocation.TypeSNI)
	leases := allocations.Poll("edge-agent-01", 1, 0)
	if len(leases) != 1 {
		t.Fatalf("expected one assigned lease, got %d", len(leases))
	}
	if !allocations.Activate(leases[0].ID, "edge-agent-01", "185.80.196.101", "playstation.com", "") {
		t.Fatal("failed to activate SNI lease")
	}

	handler := NewHandler(nil, rules, allocations, nil, 30)
	for _, hostname := range []string{
		"playstation.com.",
		"www.playstation.com.",
		"store.playstation.com.",
		"api.store.playstation.com.",
	} {
		t.Run(hostname, func(t *testing.T) {
			req := new(mdns.Msg)
			req.SetQuestion(hostname, mdns.TypeA)
			writer := &capturingDNSWriter{}
			handler.ServeDNS(writer, req)

			if writer.response == nil {
				t.Fatal("expected DNS response")
			}
			if writer.response.Rcode != mdns.RcodeSuccess {
				t.Fatalf("unexpected response code: %d", writer.response.Rcode)
			}
			if len(writer.response.Answer) != 1 {
				t.Fatalf("expected exactly one A answer, got %d", len(writer.response.Answer))
			}
			a, ok := writer.response.Answer[0].(*mdns.A)
			if !ok {
				t.Fatalf("expected A record, got %T", writer.response.Answer[0])
			}
			if a.Hdr.Name != hostname {
				t.Errorf("answer name = %q, want %q", a.Hdr.Name, hostname)
			}
			if got := a.A.String(); got != "185.80.196.101" {
				t.Errorf("answer address = %q, want edge allocation", got)
			}
			if a.Hdr.Ttl != 30 {
				t.Errorf("answer TTL = %d, want 30", a.Hdr.Ttl)
			}
		})
	}
}
