package dns

import (
	"net"
	"testing"

	mdns "github.com/miekg/dns"

	"github.com/wraplink/dns-control/internal/allocation"
	"github.com/wraplink/dns-control/internal/domain"
)

func TestSpecificIPDNSAnswers(t *testing.T) {
	for _, tc := range []struct {
		address string
		qtype   uint16
		want    string
	}{
		{"192.0.2.50", mdns.TypeA, "192.0.2.50"},
		{"2001:db8::50", mdns.TypeAAAA, "2001:db8::50"},
	} {
		store, err := domain.NewStore([]domain.Rule{{Domain: "special.example", Mode: domain.ModeSpecificIP, SpecificIP: tc.address}})
		if err != nil {
			t.Fatal(err)
		}
		h := NewHandler(nil, store, allocation.NewStore(), nil, 30)
		req := new(mdns.Msg)
		req.SetQuestion("special.example.", tc.qtype)
		w := &capturingDNSWriter{}
		h.ServeDNS(w, req)
		if w.response == nil || w.response.Rcode != mdns.RcodeSuccess || len(w.response.Answer) != 1 {
			t.Fatalf("unexpected answer for %s: %+v", tc.address, w.response)
		}
		var got net.IP
		switch rr := w.response.Answer[0].(type) {
		case *mdns.A:
			got = rr.A
		case *mdns.AAAA:
			got = rr.AAAA
		default:
			t.Fatalf("unexpected record type %T", rr)
		}
		if !got.Equal(net.ParseIP(tc.want)) {
			t.Fatalf("got %v, want %s", got, tc.want)
		}
	}
}

func TestManagedDomainWithoutAllocationFailsClosed(t *testing.T) {
	store, err := domain.NewStore([]domain.Rule{{Domain: "managed.example", Mode: domain.ModeHijack}})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, store, allocation.NewStore(), nil, 30)
	req := new(mdns.Msg)
	req.SetQuestion("managed.example.", mdns.TypeA)
	w := &capturingDNSWriter{}
	h.ServeDNS(w, req)
	if w.response == nil || w.response.Rcode != mdns.RcodeServerFailure {
		t.Fatalf("expected SERVFAIL, got %+v", w.response)
	}
}

func TestManagedDNSPolicyMatrix(t *testing.T) {
	cases := []struct {
		name string
		rule domain.Rule
		qtype uint16
		wantRcode int
		wantAnswers int
	}{
		{name: "specific IPv4 A", rule: domain.Rule{Domain: "specific.example", Mode: domain.ModeSpecificIP, SpecificIP: "192.0.2.10"}, qtype: mdns.TypeA, wantRcode: mdns.RcodeSuccess, wantAnswers: 1},
		{name: "specific IPv4 AAAA empty", rule: domain.Rule{Domain: "specific.example", Mode: domain.ModeSpecificIP, SpecificIP: "192.0.2.10"}, qtype: mdns.TypeAAAA, wantRcode: mdns.RcodeSuccess},
		{name: "SNI unallocated fails closed", rule: domain.Rule{Domain: "sni.example", Mode: domain.ModeHijack}, qtype: mdns.TypeA, wantRcode: mdns.RcodeServerFailure},
		{name: "SNI AAAA empty", rule: domain.Rule{Domain: "sni.example", Mode: domain.ModeHijack}, qtype: mdns.TypeAAAA, wantRcode: mdns.RcodeSuccess},
		{name: "route unallocated fails closed", rule: domain.Rule{Domain: "route.example", Mode: domain.ModeRoute}, qtype: mdns.TypeA, wantRcode: mdns.RcodeServerFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := domain.NewStore([]domain.Rule{tc.rule})
			if err != nil { t.Fatal(err) }
			h := NewHandler(nil, store, allocation.NewStore(), nil, 30)
			req := new(mdns.Msg)
			req.SetQuestion(mdns.Fqdn(tc.rule.Domain), tc.qtype)
			w := &capturingDNSWriter{}
			h.ServeDNS(w, req)
			if w.response == nil { t.Fatal("missing DNS response") }
			if w.response.Rcode != tc.wantRcode || len(w.response.Answer) != tc.wantAnswers {
				t.Fatalf("rcode=%d answers=%d; want rcode=%d answers=%d", w.response.Rcode, len(w.response.Answer), tc.wantRcode, tc.wantAnswers)
			}
		})
	}
}
