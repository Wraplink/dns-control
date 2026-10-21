package api

import (
	"io"
	"net/http"

	mdns "github.com/miekg/dns"
)

// resolveDNS resolves origin DNS only; it deliberately does not apply
// managed hijack policies, avoiding parent-child forwarding loops.
func (h *Handler) resolveDNS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.resolver == nil {
		http.Error(w, "resolver unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get("Content-Type") != "application/dns-message" {
		http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
		return
	}
	payload, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(payload) > 65536 {
		http.Error(w, "invalid DNS payload", http.StatusBadRequest)
		return
	}
	var query mdns.Msg
	if err := query.Unpack(payload); err != nil || query.Response || len(query.Question) != 1 {
		http.Error(w, "invalid DNS question", http.StatusBadRequest)
		return
	}
	response, err := h.resolver.Resolve(&query)
	if err != nil {
		http.Error(w, "upstream DNS resolution failed", http.StatusBadGateway)
		return
	}
	wire, err := response.Pack()
	if err != nil {
		http.Error(w, "invalid upstream response", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/dns-message")
	_, _ = w.Write(wire)
}
