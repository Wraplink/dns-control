package dns

import (
	"net"
	"testing"

	mdns "github.com/miekg/dns"
)

type remoteDNSWriter struct {
	capturingDNSWriter
}

func (w *remoteDNSWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(198, 51, 100, 9), Port: 45678}
}

func TestLoopbackDNSClientDetection(t *testing.T) {
	if !isLoopbackDNSClient(&capturingDNSWriter{}) {
		t.Fatal("expected IPv4 localhost to be recognized")
	}
	if isLoopbackDNSClient(&remoteDNSWriter{}) {
		t.Fatal("remote DNS client must not bypass entitlements")
	}
	if isLoopbackDNSClient(nil) {
		t.Fatal("nil writer must not bypass entitlements")
	}
}

func TestLoopbackDNSClientTCP(t *testing.T) {
	writer := &tcpLoopbackDNSWriter{}
	if !isLoopbackDNSClient(writer) {
		t.Fatal("expected IPv6 TCP loopback to be recognized")
	}
}

type tcpLoopbackDNSWriter struct {
	capturingDNSWriter
}

func (w *tcpLoopbackDNSWriter) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("::1"), Port: 12345}
}

var _ mdns.ResponseWriter = (*remoteDNSWriter)(nil)
