package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
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
		Store:         fakePublicIPStore{},
		Authenticator: auth,
		Verifier:      fakePublicIPVerifier{},
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

func TestPublicIPReportRouteRequiresEveryDependency(t *testing.T) {
	legacy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	auth, err := publicipsync.NewNodeAuthenticator(map[string]string{"edge-eu-01": strings.Repeat("x", 48)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		handler publicipsync.ReportHandler
	}{
		{name: "missing store", handler: publicipsync.ReportHandler{Authenticator: auth, Verifier: fakePublicIPVerifier{}}},
		{name: "missing authenticator", handler: publicipsync.ReportHandler{Store: fakePublicIPStore{}, Verifier: fakePublicIPVerifier{}}},
		{name: "missing verifier", handler: publicipsync.ReportHandler{Store: fakePublicIPStore{}, Authenticator: auth}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := WithPublicIPReportEndpoint(legacy, tc.handler)
			w := httptest.NewRecorder()
			wrapped.ServeHTTP(w, httptest.NewRequest(http.MethodPost, publicIPReportPath, nil))
			if w.Code != http.StatusTeapot {
				t.Fatalf("incomplete security dependencies mounted route: %d", w.Code)
			}
		})
	}
}

func TestPublicIPReportRoutePersistsAuthenticatedReport(t *testing.T) {
	ctx := context.Background()
	store, err := publicipsync.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	op := publicipsync.Operation{
		SubscriptionID: uuid.New(),
		PublicIPID:     uuid.New(),
		IPAddress:      "8.8.8.8",
		Revision:       1,
		AttemptID:      uuid.New(),
	}
	if err := store.Begin(ctx, op, []string{"edge-eu-01"}); err != nil {
		t.Fatal(err)
	}
	auth, err := publicipsync.NewNodeAuthenticator(map[string]string{"edge-eu-01": strings.Repeat("x", 48)})
	if err != nil {
		t.Fatal(err)
	}
	legacy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	wrapped := WithPublicIPReportEndpoint(legacy, publicipsync.ReportHandler{
		Store:         store,
		Authenticator: auth,
		Verifier:      acceptingPublicIPVerifier{},
	})
	report := publicipsync.NodeReport{
		NodeID:         "edge-eu-01",
		SubscriptionID: op.SubscriptionID,
		PublicIPID:     op.PublicIPID,
		Revision:       op.Revision,
		AttemptID:      op.AttemptID,
		Applied:        true,
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, publicIPReportPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 48))
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("verified report rejected: %d %s", w.Code, w.Body.String())
	}
	snapshot, err := store.Snapshot(ctx, op.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if complete, err := snapshot.Complete(); err != nil || !complete {
		t.Fatalf("verified route report not persisted: complete=%v err=%v", complete, err)
	}
}

type acceptingPublicIPVerifier struct{}

func (acceptingPublicIPVerifier) VerifyApplied(_ context.Context, _ string, _ publicipsync.Operation) (bool, error) {
	return true, nil
}

func TestPublicIPReportRouteRejectsUnverifiedDataplane(t *testing.T) {
	ctx := context.Background()
	store, err := publicipsync.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	op := publicipsync.Operation{
		SubscriptionID: uuid.New(),
		PublicIPID:     uuid.New(),
		IPAddress:      "8.8.8.8",
		Revision:       1,
		AttemptID:      uuid.New(),
	}
	if err := store.Begin(ctx, op, []string{"edge-eu-01"}); err != nil {
		t.Fatal(err)
	}
	auth, err := publicipsync.NewNodeAuthenticator(map[string]string{"edge-eu-01": strings.Repeat("x", 48)})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := WithPublicIPReportEndpoint(http.NotFoundHandler(), publicipsync.ReportHandler{
		Store:         store,
		Authenticator: auth,
		Verifier:      fakePublicIPVerifier{},
	})
	report := publicipsync.NodeReport{
		NodeID:         "edge-eu-01",
		SubscriptionID: op.SubscriptionID,
		PublicIPID:     op.PublicIPID,
		Revision:       op.Revision,
		AttemptID:      op.AttemptID,
		Applied:        true,
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, publicIPReportPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 48))
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)
	if w.Code == http.StatusAccepted {
		t.Fatal("unverified dataplane was acknowledged")
	}
	snapshot, err := store.Snapshot(ctx, op.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if complete, err := snapshot.Complete(); err != nil || complete {
		t.Fatalf("unverified dataplane completed operation: complete=%v err=%v", complete, err)
	}
}
