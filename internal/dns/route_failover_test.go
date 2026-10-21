package dns

import (
	"net"
	"testing"
	"time"

	mdns "github.com/miekg/dns"

	"github.com/wraplink/dns-control/internal/allocation"
	"github.com/wraplink/dns-control/internal/domain"
)

func TestRouteDNSRoundRobinAndFailover(t *testing.T) {
	rules, err := domain.NewStore([]domain.Rule{{
		Domain: "route.example", Mode: domain.ModeRoute,
		Route: &domain.RoutePolicy{DestinationIP: "203.0.113.20", Protocol: "tcp"},
	}})
	if err != nil { t.Fatal(err) }
	leases := allocation.NewStore(time.Minute)
	leases.ReconcilePolicies(rules.List())
	first := leases.Poll("edge-a", 0, 1)
	if len(first) != 1 || !leases.Activate(first[0].ID, "edge-a", "192.0.2.10", "", "route-a") { t.Fatal("activate first route") }
	second := leases.Poll("edge-b", 0, 1)
	if len(second) != 1 || !leases.Activate(second[0].ID, "edge-b", "192.0.2.11", "", "route-b") { t.Fatal("activate replica route") }
	handler := NewHandler(nil, rules, leases, nil, 30)
	query := func() string {
		req := new(mdns.Msg)
		req.SetQuestion("route.example.", mdns.TypeA)
		w := &capturingDNSWriter{}
		handler.ServeDNS(w, req)
		if w.response == nil || len(w.response.Answer) != 1 { t.Fatalf("unexpected DNS answer: %+v", w.response) }
		return w.response.Answer[0].(*mdns.A).A.String()
	}
	a, b := query(), query()
	if a == b { t.Fatalf("round robin returned same address %s twice", a) }
	if a != "192.0.2.10" && a != "192.0.2.11" { t.Fatalf("unexpected address %s", a) }
	if !leases.Fail(first[0].ID, "edge-a") { t.Fatal("fail primary route") }
	if got := query(); got != "192.0.2.11" { t.Fatalf("failover returned %s, want 192.0.2.11", got) }
	if net.ParseIP(query()) == nil { t.Fatal("invalid route IP") }
}
