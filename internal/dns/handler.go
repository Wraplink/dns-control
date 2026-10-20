package dns

import (
	"log"
	"net"
	"strings"

	mdns "github.com/miekg/dns"

	"github.com/wraplink/dns-control/internal/allocation"
	"github.com/wraplink/dns-control/internal/domain"
	"github.com/wraplink/dns-control/internal/entitlement"
)

type Handler struct {
	resolver     *Resolver
	domains      *domain.Store
	allocations  *allocation.Store
	entitlements *entitlement.Store
	ttl          uint32
}

func NewHandler(
	resolver *Resolver,
	domains *domain.Store,
	allocations *allocation.Store,
	entitlements *entitlement.Store,
	ttl uint32,
) *Handler {
	return &Handler{
		resolver:     resolver,
		domains:      domains,
		allocations:  allocations,
		entitlements: entitlements,
		ttl:          ttl,
	}
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
	if h.entitlements != nil && !h.entitlements.Allowed(responseWriterIPv4(w)) {
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

	if h.entitlements != nil {
		clientIP := responseWriterIPv4(w)
		entitlement, allowed := h.entitlements.Get(clientIP)
		if !allowed {
			log.Printf(
				"dns: direct fallback for non-entitled client=%q domain=%q mode=%q",
				ipString(clientIP),
				domainName,
				rule.Mode,
			)
			h.handleNormal(w, req)
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
	case domain.ModeHijack:
		h.handleHijack(w, req, rule.Domain)
	case domain.ModeRoute:
		h.handleRoute(w, req, rule.Domain)
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
		h.handleNormal(w, req)
		return
	}

	allocation, ok := h.allocations.Get(domainName)
	if !ok {
		log.Printf("dns: no SNI allocation for %q", domainName)
		h.handleNormal(w, req)
		return
	}

	ip := net.ParseIP(allocation.Address)
	if ip == nil || ip.To4() == nil {
		log.Printf(
			"dns: invalid SNI allocation address domain=%q address=%q",
			domainName,
			allocation.Address,
		)
		h.handleNormal(w, req)
		return
	}

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
		h.handleNormal(w, req)
		return
	}

	ip := net.ParseIP(allocation.Address)
	if ip == nil || ip.To4() == nil {
		log.Printf(
			"dns: invalid ROUTE address domain=%q address=%q",
			domainName,
			allocation.Address,
		)
		h.handleNormal(w, req)
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
