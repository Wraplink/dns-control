package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wraplink/dns-control/internal/dns"
)

func TestResolveRecordsValidation(t *testing.T) {
	h := &Handler{}
	tests := []struct {
		name   string
		method string
		body   string
		want   int
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

func TestResolveRecordsBadInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"invalid JSON", "{"},
		{"empty domain", `{"domain":""}`},
		{"invalid domain", `{"domain":"invalid..example"}`},
		{"unsupported type", `{"domain":"example.com","types":["AXFR"]}`},
		{"too many types", `{"domain":"example.com","types":["A","A","A","A","A","A","A","A","A","A","A"]}`},
	}
	h := &Handler{resolver: dns.NewResolver([]string{"127.0.0.1:53"})}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/dns/resolve-records", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.resolveRecords(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
