package publicipsync

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// ReportHandler accepts node-specific acknowledgements only when the caller
// explicitly wires a trusted node authenticator and independent verifier.
// This handler is not registered in the production DNS Control router yet.
type ReportHandler struct {
	Store         DurableStore
	Tracker       *Tracker
	Authenticator *NodeAuthenticator
	Verifier      DataplaneVerifier
}

func (h ReportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.Store == nil || h.Authenticator == nil || h.Verifier == nil {
		http.Error(w, "acknowledgement service unavailable", http.StatusServiceUnavailable)
		return
	}
	node, err := h.Authenticator.Authenticate(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var report NodeReport
	if err := decoder.Decode(&report); err != nil {
		http.Error(w, "invalid report", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		http.Error(w, "multiple JSON values", http.StatusBadRequest)
		return
	} else if !errors.Is(err, io.EOF) {
		http.Error(w, "invalid trailing JSON", http.StatusBadRequest)
		return
	}
	if err := SaveDataplaneVerified(r.Context(), h.Store, node, report, h.Verifier); err != nil {
		if errors.Is(err, ErrNoPendingOperation) || errors.Is(err, ErrDataplaneUnverified) || errors.Is(err, ErrIncomplete) {
			http.Error(w, "acknowledgement rejected", http.StatusConflict)
			return
		}
		http.Error(w, "acknowledgement storage or verification unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("{\"accepted\":true}\n"))
}
