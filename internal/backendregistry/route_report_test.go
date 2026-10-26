package backendregistry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouteReportProtocol(t *testing.T) {
	tests := []struct {
		name string
		active bool
		status int
		wantStale bool
		wantError bool
	}{
		{name: "activate", active: true, status: http.StatusOK},
		{name: "withdraw", active: false, status: http.StatusOK},
		{name: "superseded", active: false, status: http.StatusConflict, wantStale: true, wantError: true},
		{name: "transient failure", active: false, status: http.StatusServiceUnavailable, wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/internal/network/route-assignments/report" || r.Method != http.MethodPost {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("X-Network-Control-Key"); got != "secret" {
					t.Errorf("control key = %q", got)
				}
				var report RouteActivation
				if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
					t.Errorf("decode: %v", err)
				}
				if report.Domain != "example.com" || report.AgentID != "edge-1" || report.PublicIP != "192.0.2.10" || report.Generation != 7 || report.Active != tc.active {
					t.Errorf("unexpected report: %+v", report)
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			err := NewClient(server.URL, "secret").ReportRouteActivation(context.Background(), RouteActivation{
				Domain: "example.com", AgentID: "edge-1", PublicIP: "192.0.2.10", Generation: 7, Active: tc.active,
			})
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, wantError = %v", err, tc.wantError)
			}
			if errors.Is(err, ErrStaleAssignment) != tc.wantStale {
				t.Errorf("stale error = %v, want %v", err, tc.wantStale)
			}
		})
	}
}
