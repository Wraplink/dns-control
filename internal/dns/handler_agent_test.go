package dns

import (
    "io"
    "net"
    "net/http"
    "net/http/httptest"
    "testing"

    mdns "github.com/miekg/dns"
    "github.com/wraplink/dns-control/internal/allocation"
    "github.com/wraplink/dns-control/internal/domain"
)

type agentTestWriter struct { response *mdns.Msg }
func (w *agentTestWriter) LocalAddr() net.Addr { return &net.UDPAddr{IP: net.IPv4(127,0,0,1), Port:53} }
func (w *agentTestWriter) RemoteAddr() net.Addr { return &net.UDPAddr{IP: net.IPv4(127,0,0,1), Port:12345} }
func (w *agentTestWriter) WriteMsg(m *mdns.Msg) error { w.response = m; return nil }
func (w *agentTestWriter) Write(p []byte) (int,error) { var m mdns.Msg; if err:=m.Unpack(p); err!=nil{return 0,err}; w.response=&m; return len(p),nil }
func (w *agentTestWriter) Close() error { return nil }
func (w *agentTestWriter) TsigStatus() error { return nil }
func (w *agentTestWriter) TsigTimersOnly(bool) {}
func (w *agentTestWriter) Hijack() {}

func TestNormalDNSUsesConfiguredAgent(t *testing.T) {
    calls:=0
    server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
        calls++
        if r.URL.Path!="/api/v1/dns/resolve" || r.Method!=http.MethodPost || r.Header.Get("Authorization")!="Bearer test-secret" || r.Header.Get("Content-Type")!="application/dns-message" { t.Errorf("unexpected request: %s %s",r.Method,r.URL.Path); w.WriteHeader(http.StatusUnauthorized);return }
        body,err:=io.ReadAll(r.Body);if err!=nil {t.Error(err);return}
        var q mdns.Msg
        if err:=q.Unpack(body);err!=nil {t.Error(err);return}
        resp:=new(mdns.Msg);resp.SetReply(&q)
        resp.Answer=[]mdns.RR{&mdns.A{Hdr:mdns.RR_Header{Name:q.Question[0].Name,Rrtype:mdns.TypeA,Class:mdns.ClassINET,Ttl:123},A:net.IPv4(203,0,113,8)}}
        wire,err:=resp.Pack();if err!=nil {t.Error(err);return}
        w.Header().Set("Content-Type","application/dns-message");_,_=w.Write(wire)
    }))
    defer server.Close()
    resolver:=NewResolver([]string{"127.0.0.1:1"})
    resolver.SetOriginAgent(server.URL,"test-secret")
    domains,err:=domain.NewStore(nil);if err!=nil {t.Fatal(err)}
    handler:=NewHandler(resolver,domains,allocation.NewStore(0),nil,60)
    q:=new(mdns.Msg);q.SetQuestion("microsoft.com.",mdns.TypeA)
    writer:=&agentTestWriter{}
    handler.ServeDNS(writer,q)
    if calls!=1 {t.Fatalf("expected one agent request, got %d",calls)}
    if writer.response==nil || writer.response.Rcode!=mdns.RcodeSuccess || len(writer.response.Answer)!=1 {t.Fatalf("unexpected DNS response: %+v",writer.response)}
    if a,ok:=writer.response.Answer[0].(*mdns.A);!ok || !a.A.Equal(net.IPv4(203,0,113,8)) {t.Fatalf("unexpected answer: %v",writer.response.Answer)}
}

func TestNormalDNSAgentFailureDoesNotFallBack(t *testing.T) {
    server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusServiceUnavailable)}))
    defer server.Close()
    resolver:=NewResolver([]string{"1.1.1.1:53"})
    resolver.SetOriginAgent(server.URL,"test-secret")
    domains,err:=domain.NewStore(nil);if err!=nil {t.Fatal(err)}
    handler:=NewHandler(resolver,domains,allocation.NewStore(0),nil,60)
    q:=new(mdns.Msg);q.SetQuestion("microsoft.com.",mdns.TypeA)
    writer:=&agentTestWriter{}
    handler.ServeDNS(writer,q)
    if writer.response==nil || writer.response.Rcode!=mdns.RcodeServerFailure {t.Fatalf("expected SERVFAIL without local fallback, got %+v",writer.response)}
}
