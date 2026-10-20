package dns

import (
	"net"

	mdns "github.com/miekg/dns"
)

type capturingDNSWriter struct {
	response *mdns.Msg
}

func (w *capturingDNSWriter) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53}
}

func (w *capturingDNSWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
}

func (w *capturingDNSWriter) WriteMsg(msg *mdns.Msg) error {
	w.response = msg.Copy()
	return nil
}

func (w *capturingDNSWriter) Write(b []byte) (int, error) {
	return len(b), nil
}

func (w *capturingDNSWriter) Close() error {
	return nil
}

func (w *capturingDNSWriter) TsigStatus() error {
	return nil
}

func (w *capturingDNSWriter) TsigTimersOnly(bool) {}

func (w *capturingDNSWriter) Hijack() {}
