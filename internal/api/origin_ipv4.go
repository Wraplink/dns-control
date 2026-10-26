package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	mdns "github.com/miekg/dns"
)

// resolveOriginIPv4 bypasses managed WrapLink policies. The existing API
// bearer authentication applies; this endpoint must not be exposed publicly.
func (h *Handler) resolveOriginIPv4(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.resolver == nil {
		http.Error(w, "resolver unavailable", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Domain string `json:"domain"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(req.Domain)), ".")
	_, valid := mdns.IsDomainName(domain)
	if domain == "" || len(domain) > 253 || !valid {
		http.Error(w, "invalid domain", http.StatusBadRequest)
		return
	}
	msg := new(mdns.Msg)
	msg.SetQuestion(mdns.Fqdn(domain), mdns.TypeA)
	answer, err := h.resolver.ResolveContext(r.Context(), msg)
	if err != nil || answer == nil || answer.Rcode != mdns.RcodeSuccess {
		http.Error(w, "origin resolution failed", http.StatusBadGateway)
		return
	}
	for _, record := range answer.Answer {
		if a, ok := record.(*mdns.A); ok && a.A.To4() != nil {
			ip := net.IP(a.A.To4()).String()
			writeJSON(w, http.StatusOK, map[string]string{"domain": domain, "destination_ip": ip})
			return
		}
	}
	http.Error(w, "no original IPv4 address", http.StatusBadGateway)
}
