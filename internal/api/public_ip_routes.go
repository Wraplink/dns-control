package api

import (
	"net/http"

	"github.com/wraplink/dns-control/internal/publicipsync"
)

const publicIPReportPath = "/api/v1/public-ips/agent/report"

// WithPublicIPReportEndpoint mounts a node-authenticated report handler outside
// the legacy shared-token middleware. It is deliberately opt-in: production
// must provide a durable store, per-node credentials and an independent
// dataplane verifier before mounting the route.
func WithPublicIPReportEndpoint(existing http.Handler, reports publicipsync.ReportHandler) http.Handler {
	if existing == nil || reports.Store == nil || reports.Authenticator == nil || reports.Verifier == nil {
		return existing
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == publicIPReportPath {
			reports.ServeHTTP(w, r)
			return
		}
		existing.ServeHTTP(w, r)
	})
}
