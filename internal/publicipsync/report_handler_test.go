package publicipsync

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReportHandlerFailClosed(t *testing.T) {
	op, nodes, reports := fixture()
	tracker := NewTracker()
	if err := tracker.Begin(op, nodes); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("z", 48)
	auth, err := NewNodeAuthenticator(map[string]string{nodes[0]: token})
	if err != nil {
		t.Fatal(err)
	}
	verifier := verifyFunc(func(context.Context, string, Operation) (bool, error) { return true, nil })
	body, err := json.Marshal(reports[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		handler    ReportHandler
		credential string
		body       []byte
		want       int
	}{
		{"missing verifier", ReportHandler{Tracker: tracker, Authenticator: auth}, token, body, http.StatusServiceUnavailable},
		{"missing credentials", ReportHandler{Tracker: tracker, Authenticator: auth, Verifier: verifier}, "", body, http.StatusUnauthorized},
		{"invalid JSON", ReportHandler{Tracker: tracker, Authenticator: auth, Verifier: verifier}, token, []byte("{"), http.StatusBadRequest},
		{"unknown field", ReportHandler{Tracker: tracker, Authenticator: auth, Verifier: verifier}, token, append(body[:len(body)-1:len(body)-1], []byte(",\"unknown\":1}")...), http.StatusBadRequest},
		{"oversized", ReportHandler{Tracker: tracker, Authenticator: auth, Verifier: verifier}, token, []byte(strings.Repeat("x", 4097)), http.StatusBadRequest},
		{"trailing JSON", ReportHandler{Tracker: tracker, Authenticator: auth, Verifier: verifier}, token, append(append([]byte{}, body...), []byte(" {}")...), http.StatusBadRequest},
		{"valid", ReportHandler{Tracker: tracker, Authenticator: auth, Verifier: verifier}, token, body, http.StatusAccepted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(tc.body))
			if tc.credential != "" {
				req.Header.Set("Authorization", "Bearer "+tc.credential)
			}
			w := httptest.NewRecorder()
			tc.handler.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}
