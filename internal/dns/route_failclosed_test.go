package dns

import (
 "testing"

 mdns "github.com/miekg/dns"
 "github.com/wraplink/dns-control/internal/allocation"
 "github.com/wraplink/dns-control/internal/domain"
)

func TestFullRouteNeverFallsBackToOriginalIPv6(t *testing.T) {
 store, err := domain.NewStore([]domain.Rule{{
  Domain: "route.example",
  Mode: domain.ModeRoute,
  Route: &domain.RoutePolicy{DestinationIP: "", Protocol: "tcp_udp"},
 }})
 if err != nil { t.Fatal(err) }
 handler := NewHandler(nil, store, allocation.NewStore(), nil, 30)
 for _, qtype := range []uint16{mdns.TypeA, mdns.TypeAAAA} {
  request := new(mdns.Msg)
  request.SetQuestion("route.example.", qtype)
  writer := &capturingDNSWriter{}
  handler.ServeDNS(writer, request)
  if writer.response == nil || writer.response.Rcode != mdns.RcodeServerFailure {
   t.Fatalf("query type %d: expected fail-closed SERVFAIL, got %+v", qtype, writer.response)
  }
 }
}
