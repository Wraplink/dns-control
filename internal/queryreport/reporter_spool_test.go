package queryreport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestReporterReplaysPersistedBatchAfterRestart(t *testing.T) {
	dir := t.TempDir()
	spool, err := NewSpool(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{EventID: "persisted-event", ObservedAt: time.Now().UTC(), Domain: "example.org", QType: "A", RCode: "NOERROR", ResolutionMode: "normal"}
	if _, err := spool.Save([]Event{event}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Events []Event `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Events) != 1 || payload.Events[0].EventID != event.EventID {
			t.Errorf("invalid replay payload: %+v err=%v", payload, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	reporter := New(server.URL, "secret", "dns-01", 4)
	reporter.SetSpool(spool)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reporter.Run(ctx)
		close(done)
	}()
	deadline := time.After(5 * time.Second)
	for reporter.Delivered() != 1 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("persisted event not replayed")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
	files, err := spool.Files()
	if err != nil || len(files) != 0 || calls.Load() != 1 || reporter.Pending() != 0 {
		t.Fatalf("files=%v err=%v calls=%d pending=%d", files, err, calls.Load(), reporter.Pending())
	}
}
