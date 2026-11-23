package publicipsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDurableReportHandlerPersists(t *testing.T) {
	op, nodes, reports := fixture()
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Begin(context.Background(), op, nodes[:1]); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("q", 48)
	auth, err := NewNodeAuthenticator(map[string]string{nodes[0]: token})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(reports[0])
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	ReportHandler{Store: store, Authenticator: auth, Verifier: verifyFunc(func(context.Context, string, Operation) (bool, error) {
		return true, nil
	})}.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	restarted, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := restarted.Snapshot(context.Background(), op.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if complete, err := snapshot.Complete(); err != nil || !complete {
		t.Fatalf("durable report not recovered: complete=%v err=%v", complete, err)
	}
}

func TestSaveDataplaneVerifiedRejectsUnverifiedAndStale(t *testing.T) {
	ctx := context.Background()
	op, nodes, reports := fixture()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Begin(ctx, op, nodes[:1]); err != nil {
		t.Fatal(err)
	}
	node := AuthenticatedNode{ID: nodes[0]}
	denied := verifyFunc(func(context.Context, string, Operation) (bool, error) { return false, nil })
	if err := SaveDataplaneVerified(ctx, store, node, reports[0], denied); !errors.Is(err, ErrDataplaneUnverified) {
		t.Fatalf("unverified report accepted: %v", err)
	}
	snapshot, err := store.Snapshot(ctx, op.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.VerifiedReports) != 0 {
		t.Fatal("unverified report persisted")
	}
	next := op
	next.Revision++
	if err := store.Begin(ctx, next, nodes[:1]); err != nil {
		t.Fatal(err)
	}
	approved := verifyFunc(func(context.Context, string, Operation) (bool, error) { return true, nil })
	if err := SaveDataplaneVerified(ctx, store, node, reports[0], approved); !errors.Is(err, ErrNoPendingOperation) {
		t.Fatalf("stale report accepted: %v", err)
	}
	snapshot, err = store.Snapshot(ctx, op.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.VerifiedReports) != 0 {
		t.Fatal("stale report persisted")
	}
}
