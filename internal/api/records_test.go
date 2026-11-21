package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveRecordsValidation(t *testing.T) {
	h := &Handler{}
	tests := []struct {
		name string
		method string
		body string
		want int
	}{
		{"wrong method", http.MethodGet, "", http.StatusMethodNotAllowed},
		{"no resolver", http.MethodPost, `{"domain":"example.com"}`, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/v1/dns/resolve-records", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.resolveRecords(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}
