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
		qtype uint16
		want string
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
