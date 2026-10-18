package dns

import (
	"net"
	"testing"
	"time"

	mdns "github.com/miekg/dns"

	"github.com/wraplink/dns-control/internal/allocation"
	"github.com/wraplink/dns-control/internal/domain"
	"github.com/wraplink/dns-control/internal/entitlement"
)

type memoryDNSWriter struct {
	clientIP net.IP
	response *mdns.Msg
}

func (w *memoryDNSWriter) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53}
}
func (w *memoryDNSWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: w.clientIP, Port: 54444}
}
func (w *memoryDNSWriter) WriteMsg(msg *mdns.Msg) error {
	w.response = msg.Copy()
	return nil
}
func (w *memoryDNSWriter) Write([]byte) (int, error) { return 0, nil }
func (w *memoryDNSWriter) Close() error { return nil }
func (w *memoryDNSWriter) TsigStatus() error { return nil }
func (w *memoryDNSWriter) TsigTimersOnly(bool) {}
func (w *memoryDNSWriter) Hijack() {}

func TestPerClientModeChangesActualDNSAnswer(t *testing.T) {
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	upstream := &mdns.Server{
		PacketConn: packetConn,
		Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
			m := new(mdns.Msg)
			m.SetReply(r)
			m.Answer = []mdns.RR{&mdns.A{
				Hdr: mdns.RR_Header{Name: r.Question[0].Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 30},
				A:   net.ParseIP("192.0.2.10"),
			}}
			_ = w.WriteMsg(m)
		}),
	}
	go func() { _ = upstream.ActivateAndServe() }()
	defer upstream.Shutdown()

	rules, err := domain.NewStore([]domain.Rule{{
		Domain: "playstation.com", Mode: domain.ModeHijack,
		ConnectionModes: []string{"gaming"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	leases := allocation.NewStore()
	leases.EnsurePending("playstation.com", allocation.TypeSNI)
	pending := leases.Poll("test-edge", 1, 0)
	if len(pending) != 1 || !leases.Activate(pending[0].ID, "test-edge", "198.51.100.30", "playstation.com", "") {
		t.Fatal("could not activate managed DNS lease")
	}

	access := entitlement.NewStore(time.Minute)
	expires := time.Now().Add(time.Hour)
	access.Replace(entitlement.Snapshot{Entitlements: []entitlement.Item{
		{ClientIP: "203.0.113.10", ConnectionMode: "gaming", RemainingBytes: 1000, ExpiresAt: expires},
		{ClientIP: "203.0.113.11", ConnectionMode: "streaming", RemainingBytes: 1000, ExpiresAt: expires},
		{ClientIP: "203.0.113.12", ConnectionMode: "smart", RemainingBytes: 1000, ExpiresAt: expires},
	}})

	handler := NewHandler(NewResolver([]string{packetConn.LocalAddr().String()}), rules, leases, access, 30)
	for _, tc := range []struct {
		client, want string
	}{
		{"203.0.113.10", "198.51.100.30"},
		{"203.0.113.11", "192.0.2.10"},
		{"203.0.113.12", "198.51.100.30"},
		{"203.0.113.13", "192.0.2.10"},
	} {
		t.Run(tc.client, func(t *testing.T) {
			w := &memoryDNSWriter{clientIP: net.ParseIP(tc.client)}
			q := new(mdns.Msg)
			q.SetQuestion("playstation.com.", mdns.TypeA)
			handler.ServeDNS(w, q)
			if w.response == nil || len(w.response.Answer) != 1 {
				t.Fatalf("no DNS answer: %#v", w.response)
			}
			answer, ok := w.response.Answer[0].(*mdns.A)
			if !ok || answer.A.String() != tc.want {
				t.Fatalf("got %v, want A %s", w.response.Answer, tc.want)
			}
		})
	}
}
