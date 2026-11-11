package dns

import (
	"net"
	"testing"

	mdns "github.com/miekg/dns"

	"github.com/wraplink/dns-control/internal/domain"
)

type modeCaptureWriter struct {
	mode string
	msg  *mdns.Msg
}

func (w *modeCaptureWriter) SetResolutionMode(mode string) {
	w.mode = mode
}
func (w *modeCaptureWriter) LocalAddr() net.Addr {
	return &net.UDPAddr{}
}
func (w *modeCaptureWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{}
}
func (w *modeCaptureWriter) WriteMsg(msg *mdns.Msg) error {
	w.msg = msg
	return nil
}
func (w *modeCaptureWriter) Write([]byte) (int, error) { return 0, nil }
func (w *modeCaptureWriter) Close() error              { return nil }
func (w *modeCaptureWriter) TsigStatus() error         { return nil }
func (w *modeCaptureWriter) TsigTimersOnly(bool)       {}
func (w *modeCaptureWriter) Hijack()                   {}

func TestHandlerReportsEffectiveManagedMode(t *testing.T) {
	tests := []struct {
		name      string
		rule      domain.Rule
		qtype     uint16
		wantMode  string
		wantRcode int
	}{
		{name: "blocked", rule: domain.Rule{Domain: "example.org", Mode: domain.ModeBlock}, qtype: mdns.TypeA, wantMode: "block", wantRcode: mdns.RcodeRefused},
		{name: "hijack IPv6 suppressed", rule: domain.Rule{Domain: "example.org", Mode: domain.ModeHijack}, qtype: mdns.TypeAAAA, wantMode: "hijack", wantRcode: mdns.RcodeSuccess},
		{name: "route IPv6 rejected", rule: domain.Rule{Domain: "example.org", Mode: domain.ModeRoute, Route: &domain.RoutePolicy{Protocol: "tcp_udp"}}, qtype: mdns.TypeAAAA, wantMode: "route", wantRcode: mdns.RcodeServerFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := domain.NewStore([]domain.Rule{tt.rule})
			if err != nil {
				t.Fatal(err)
			}
			handler := &Handler{domains: store}
			writer := &modeCaptureWriter{}
			req := new(mdns.Msg)
			req.SetQuestion("example.org.", tt.qtype)
			handler.ServeDNS(writer, req)
			if writer.msg == nil || writer.msg.Rcode != tt.wantRcode || writer.mode != tt.wantMode {
				t.Fatalf("rcode=%v mode=%q, want rcode=%d mode=%q", writer.msg, writer.mode, tt.wantRcode, tt.wantMode)
			}
		})
	}
}
