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

func TestReporterKeepsSpoolOnBackendOutageThenRecovers(t *testing.T) {
	dir := t.TempDir()
	spool, err := NewSpool(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var available atomic.Bool
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if !available.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	reporter := New(server.URL, "secret", "dns-01", 8)
	reporter.SetSpool(spool)
	reporter.Enqueue(Event{
		EventID: "outage-event", ObservedAt: time.Now().UTC(),
		Domain: "example.org", QType: "A", RCode: "NOERROR",
		ResolutionMode: "normal",
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reporter.Run(ctx)
		close(done)
	}()
	deadline := time.After(8 * time.Second)
	for reporter.FailedBatches() == 0 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("reporter did not attempt delivery during outage")
		case <-time.After(20 * time.Millisecond):
		}
	}
	files, err := spool.Files()
	if err != nil || len(files) != 1 {
		cancel()
		<-done
		t.Fatalf("outage must retain persisted batch: %v %v", files, err)
	}
	available.Store(true)
	for reporter.Delivered() == 0 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("reporter did not recover")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
	files, err = spool.Files()
	if err != nil || len(files) != 0 || attempts.Load() < 2 || reporter.Dropped() != 0 {
		t.Fatalf("recovery files=%v err=%v attempts=%d dropped=%d", files, err, attempts.Load(), reporter.Dropped())
	}
}
