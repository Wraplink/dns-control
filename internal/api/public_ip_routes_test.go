package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wraplink/dns-control/internal/publicipsync"
)

func TestPublicIPReportRouteIsOptIn(t *testing.T) {
	legacy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	disabled := WithPublicIPReportEndpoint(legacy, publicipsync.ReportHandler{})
	for _, path := range []string{publicIPReportPath, "/api/v1/agent/report"} {
		w := httptest.NewRecorder()
		disabled.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != http.StatusTeapot {
			t.Fatalf("disabled route %s intercepted: %d", path, w.Code)
		}
	}
	auth, err := publicipsync.NewNodeAuthenticator(map[string]string{"edge-eu-01": strings.Repeat("x", 48)})
	if err != nil {
		t.Fatal(err)
	}
	enabled := WithPublicIPReportEndpoint(legacy, publicipsync.ReportHandler{
		Store: fakePublicIPStore{},
		Authenticator: auth,
		Verifier: fakePublicIPVerifier{},
	})
	w := httptest.NewRecorder()
	enabled.ServeHTTP(w, httptest.NewRequest(http.MethodPost, publicIPReportPath, nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing per-node token: %d", w.Code)
	}
	w = httptest.NewRecorder()
	enabled.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/agent/report", nil))
	if w.Code != http.StatusTeapot {
		t.Fatalf("legacy route intercepted: %d", w.Code)
	}
}

type fakePublicIPStore struct{ publicipsync.DurableStore }
type fakePublicIPVerifier struct{}

func (fakePublicIPVerifier) VerifyApplied(_ context.Context, _ string, _ publicipsync.Operation) (bool, error) {
	return false, nil
}
