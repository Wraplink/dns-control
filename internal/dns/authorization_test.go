package dns

import (
	"testing"
	"time"

	mdns "github.com/miekg/dns"

	"github.com/wraplink/dns-control/internal/allocation"
	"github.com/wraplink/dns-control/internal/domain"
	"github.com/wraplink/dns-control/internal/entitlement"
)

func TestUnauthorizedClientIsRefusedBeforeUpstreamResolution(t *testing.T) {
	rules, err := domain.NewStore([]domain.Rule{})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(nil, rules, allocation.NewStore(), entitlement.NewStore(time.Minute), 30)
	req := new(mdns.Msg)
	req.SetQuestion("example.org.", mdns.TypeA)
	writer := &capturingDNSWriter{}
	handler.ServeDNS(writer, req)
	if writer.response == nil || writer.response.Rcode != mdns.RcodeRefused {
		t.Fatalf("expected REFUSED for unauthorized DNS client, got %+v", writer.response)
	}
}
