package dns

import (
	"log"
	"net"
	"strings"

	mdns "github.com/miekg/dns"

	"github.com/wraplink/dns-control/internal/agentregistry"
	"github.com/wraplink/dns-control/internal/allocation"
	"github.com/wraplink/dns-control/internal/domain"
	"github.com/wraplink/dns-control/internal/entitlement"
)

type Handler struct {
	resolver             *Resolver
	agents               *agentregistry.Store
	domains              *domain.Store
	allocations          *allocation.Store
	entitlements         *entitlement.Store
	ttl                  uint32
	allowLoopbackQueries bool
}

func NewHandler(
	resolver *Resolver,
	domains *domain.Store,
	allocations *allocation.Store,
	entitlements *entitlement.Store,
	ttl uint32,
	agents ...*agentregistry.Store,
) *Handler {
	h := &Handler{
		resolver:     resolver,
		domains:      domains,
		allocations:  allocations,
		entitlements: entitlements,
		ttl:          ttl,
	}
	if len(agents) > 0 {
		h.agents = agents[0]
	}
	return h
}

// SetAllowLoopbackQueries permits local DNS diagnostics without bypassing external client authorization.
func (h *Handler) SetAllowLoopbackQueries(enabled bool) {
	h.allowLoopbackQueries = enabled
}

func (h *Handler) ServeDNS(
	w mdns.ResponseWriter,
	req *mdns.Msg,
) {
	if req == nil || len(req.Question) == 0 {
		h.serverFailure(w, req)
		return
	}

	// A configured control plane is an allowlist: unauthorized clients must
	// receive no recursive or managed DNS answers.
	if h.entitlements != nil && !h.entitlements.Allowed(responseWriterIPv4(w)) && !(h.allowLoopbackQueries && isLoopbackDNSClient(w)) {
		resp := new(mdns.Msg)
		resp.SetRcode(req, mdns.RcodeRefused)
		_ = w.WriteMsg(resp)
		return
	}

	question := req.Question[0]
	domainName := normalizeDomain(question.Name)
	rule, matched := h.domains.Match(domainName)

	if !matched || rule.Mode == domain.ModeNormal {
		h.handleNormal(w, req)
		return
	}

	if h.entitlements != nil && !(h.allowLoopbackQueries && isLoopbackDNSClient(w)) {
		entitlement, allowed := h.entitlements.Get(responseWriterIPv4(w))
		if !allowed {
			h.serverFailure(w, req)
			return
		}
		if !rule.AppliesTo(entitlement.ConnectionMode) {
			// This client selected another profile. Return the ordinary
			// upstream DNS answer; never apply someone else's mode.
			h.handleNormal(w, req)
			return
		}
	}

	switch rule.Mode {
	case domain.ModeBlock:
		resp := new(mdns.Msg)
		resp.SetRcode(req, mdns.RcodeRefused)
		_ = w.WriteMsg(resp)
	case domain.ModeHijack:
		h.handleHijack(w, req, rule.Domain)
	case domain.ModeRoute:
		h.handleRoute(w, req, rule.Domain)
	case domain.ModeSpecificIP:
		h.handleSpecificIP(w, req, rule.SpecificIP)
	default:
		log.Printf(
			"dns: unsupported mode=%q domain=%q",
			rule.Mode,
			domainName,
		)
		h.handleNormal(w, req)
	}
}

func (h *Handler) handleNormal(
	w mdns.ResponseWriter,
	req *mdns.Msg,
) {
	resp, err := h.resolver.Resolve(req)
	if err != nil {
		log.Printf("dns: upstream resolution failed: %v", err)
		h.serverFailure(w, req)
		return
	}

	if err := w.WriteMsg(resp); err != nil {
		log.Printf("dns: write normal response: %v", err)
	}
}

func (h *Handler) handleHijack(
	w mdns.ResponseWriter,
	req *mdns.Msg,
	domainName string,
) {
	if req.Question[0].Qtype != mdns.TypeA {
		if req.Question[0].Qtype == mdns.TypeAAAA {
			resp := new(mdns.Msg)
			resp.SetReply(req)
			_ = w.WriteMsg(resp)
			return
		}
		h.handleNormal(w, req)
		return
	}

	selected, ok := h.allocations.NextCandidate(domainName, func(lease allocation.Lease) bool {
		ip := net.ParseIP(lease.Address)
		return lease.Type == allocation.TypeSNI && h.healthyAgent(lease.AgentID, lease.Type) &&
			ip != nil && ip.To4() != nil
	})
	if !ok {
		log.Printf("dns: no healthy SNI allocation for %q", domainName)
		h.serverFailure(w, req)
		return
	}
	ip := net.ParseIP(selected.Address)
	h.writeAddressResponse(w, req, ip.To4())
}

func (h *Handler) handleRoute(
	w mdns.ResponseWriter,
	req *mdns.Msg,
	domainName string,
) {
	if req.Question[0].Qtype != mdns.TypeA {
		h.handleNormal(w, req)
		return
	}

	allocation, ok := h.allocations.Get(domainName)
	if !ok {
		log.Printf("dns: no ROUTE allocation for %q", domainName)
		h.serverFailure(w, req)
		return
	}

	if !h.healthyAgent(allocation.AgentID, allocation.Type) {
		h.serverFailure(w, req)
		return
	}
	ip := net.ParseIP(allocation.Address)
	if ip == nil || ip.To4() == nil {
		log.Printf(
			"dns: invalid ROUTE address domain=%q address=%q",
			domainName,
			allocation.Address,
		)
		h.serverFailure(w, req)
		return
	}

	h.writeAddressResponse(w, req, ip.To4())
}

func (h *Handler) writeAddressResponse(
	w mdns.ResponseWriter,
	req *mdns.Msg,
	ip net.IP,
) {
	resp := new(mdns.Msg)
	resp.SetReply(req)
	resp.Answer = []mdns.RR{
		&mdns.A{
			Hdr: mdns.RR_Header{
				Name:   req.Question[0].Name,
				Rrtype: mdns.TypeA,
				Class:  mdns.ClassINET,
				Ttl:    h.ttl,
			},
			A: ip,
		},
	}

	if err := w.WriteMsg(resp); err != nil {
		log.Printf("dns: write managed response: %v", err)
	}
}

func (h *Handler) serverFailure(
	w mdns.ResponseWriter,
	req *mdns.Msg,
) {
	resp := new(mdns.Msg)
	if req != nil {
		resp.SetRcode(req, mdns.RcodeServerFailure)
	} else {
		resp.SetRcode(&mdns.Msg{}, mdns.RcodeServerFailure)
	}

	if err := w.WriteMsg(resp); err != nil {
		log.Printf("dns: write SERVFAIL: %v", err)
	}
}

func responseWriterIPv4(w mdns.ResponseWriter) net.IP {
	if w == nil || w.RemoteAddr() == nil {
		return nil
	}

	switch addr := w.RemoteAddr().(type) {
	case *net.UDPAddr:
		return addr.IP.To4()
	case *net.TCPAddr:
		return addr.IP.To4()
	}

	host, _, err := net.SplitHostPort(w.RemoteAddr().String())
	if err != nil {
		return net.ParseIP(w.RemoteAddr().String()).To4()
	}
	return net.ParseIP(host).To4()
}

func ipString(ip net.IP) string {
	if ip == nil {
		return "unknown"
	}
	return ip.String()
}

func normalizeDomain(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".")
	return strings.ToLower(value)
}

func (h *Handler) handleSpecificIP(w mdns.ResponseWriter, req *mdns.Msg, address string) {
	ip := net.ParseIP(address)
	if ip == nil {
		h.serverFailure(w, req)
		return
	}
	q := req.Question[0]
	switch q.Qtype {
	case mdns.TypeA:
		if v4 := ip.To4(); v4 != nil {
			h.writeAddressResponse(w, req, v4)
			return
		}
	case mdns.TypeAAAA:
		if ip.To4() == nil {
			resp := new(mdns.Msg)
			resp.SetReply(req)
			resp.Answer = []mdns.RR{&mdns.AAAA{Hdr: mdns.RR_Header{
				Name:   q.Name,
				Rrtype: mdns.TypeAAAA,
				Class:  mdns.ClassINET,
				Ttl:    h.ttl,
			}, AAAA: ip.To16()}}
			_ = w.WriteMsg(resp)
			return
		}
	default:
		h.handleNormal(w, req)
		return
	}
	resp := new(mdns.Msg)
	resp.SetReply(req)
	_ = w.WriteMsg(resp)
}

// healthyAgent rejects stale or offline allocations when an agent registry is attached.
func (h *Handler) healthyAgent(id string, leaseType allocation.Type) bool {
	if h.agents == nil {
		return true
	}
	agent, ok := h.agents.Get(id)
	if !ok || agent.Status != agentregistry.StatusOnline {
		return false
	}
	switch leaseType {
	case allocation.TypeSNI:
		return agent.Capacity.SNI > 0
	case allocation.TypeRoute:
		return agent.Capacity.Route > 0
	default:
		return false
	}
}

func isLoopbackDNSClient(w mdns.ResponseWriter) bool {
	if w == nil || w.RemoteAddr() == nil {
		return false
	}
	switch addr := w.RemoteAddr().(type) {
	case *net.UDPAddr:
		return addr.IP.IsLoopback()
	case *net.TCPAddr:
		return addr.IP.IsLoopback()
	}
	host, _, err := net.SplitHostPort(w.RemoteAddr().String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
