package api

import (
	"encoding/json"
	"net/http"
	"strings"

	mdns "github.com/miekg/dns"
)

type recordsRequest struct {
	Domain string   `json:"domain"`
	Types  []string `json:"types,omitempty"`
}

type recordAnswer struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   uint32 `json:"ttl"`
}

type recordQueryResult struct {
	Type  string `json:"type"`
	Rcode string `json:"rcode"`
}

type recordsResponse struct {
	Domain     string              `json:"domain"`
	Rcode      string              `json:"rcode"`
	Answers    []recordAnswer      `json:"answers"`
	Authority  []recordAnswer      `json:"authority"`
	Additional []recordAnswer      `json:"additional"`
	Queries    []recordQueryResult `json:"queries"`
}

// resolveRecords performs origin (not managed-policy) DNS lookups through the
// configured origin agent, retaining all returned records and their TTLs.
// A and AAAA are queried separately as required by the DNS protocol.
func (h *Handler) resolveRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.resolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "resolver unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req recordsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	name := strings.TrimSpace(req.Domain)
	_, validName := mdns.IsDomainName(name)
	if name == "" || len(name) > 253 || !validName {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid domain"})
		return
	}
	name = mdns.Fqdn(name)
	types := req.Types
	if len(types) == 0 {
		types = []string{"A", "AAAA"}
	}
	if len(types) > 10 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many record types"})
		return
	}
	queryTypes := make([]uint16, 0, len(types))
	for _, typ := range types {
		t, ok := mdns.StringToType[strings.ToUpper(strings.TrimSpace(typ))]
		if !ok || t == mdns.TypeANY || t == mdns.TypeAXFR || t == mdns.TypeIXFR {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported DNS record type"})
			return
		}
		queryTypes = append(queryTypes, t)
	}
	out := recordsResponse{Domain: strings.TrimSuffix(name, "."), Rcode: "NOERROR", Answers: []recordAnswer{}, Authority: []recordAnswer{}, Additional: []recordAnswer{}, Queries: []recordQueryResult{}}
	for _, t := range queryTypes {
		query := new(mdns.Msg)
		query.SetQuestion(name, t)
		response, err := h.resolver.ResolveOrigin(r.Context(), query)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "origin DNS lookup failed"})
			return
		}
		rcode := mdns.RcodeToString[response.Rcode]
		out.Queries = append(out.Queries, recordQueryResult{Type: mdns.TypeToString[t], Rcode: rcode})
		if response.Rcode != mdns.RcodeSuccess {
			out.Rcode = rcode
		}
		for _, rr := range response.Answer {
			out.Answers = append(out.Answers, convertRecord(rr))
		}
		for _, rr := range response.Ns {
			out.Authority = append(out.Authority, convertRecord(rr))
		}
		for _, rr := range response.Extra {
			if rr.Header().Rrtype != mdns.TypeOPT {
				out.Additional = append(out.Additional, convertRecord(rr))
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func convertRecord(rr mdns.RR) recordAnswer {
	header := rr.Header()
	value := rr.String()
	// Preserve the full presentation-format RDATA, including MX priorities,
	// SOA fields and TXT quoting, rather than losing non-address records.
	fields := strings.Fields(value)
	if len(fields) >= 5 {
		value = strings.Join(fields[4:], " ")
	}
	return recordAnswer{Name: strings.TrimSuffix(header.Name, "."), Type: mdns.TypeToString[header.Rrtype], Value: value, TTL: header.Ttl}
}
