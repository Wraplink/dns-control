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

func TestReporterRetainsQueuedEventsDuringPersistedBatchRetry(t *testing.T) {
	spool, err := NewSpool(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	first := Event{EventID: "first", ObservedAt: time.Now().UTC(), Domain: "first.example", QType: "A", RCode: "NOERROR", ResolutionMode: "normal"}
	if _, err := spool.Save([]Event{first}); err != nil {
		t.Fatal(err)
	}
	var available atomic.Bool
	var delivered atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var payload struct {
			Events []Event `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		delivered.Add(int32(len(payload.Events)))
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
	deadline := time.After(8 * time.Second)
	for reporter.FailedBatches() == 0 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("persisted batch was not retried")
		case <-time.After(20 * time.Millisecond):
		}
	}
	reporter.Enqueue(Event{EventID: "second", ObservedAt: time.Now().UTC(), Domain: "second.example", QType: "A", RCode: "NOERROR", ResolutionMode: "normal"})
	if reporter.Dropped() != 0 {
		cancel()
		<-done
		t.Fatalf("queued event dropped during retry: %d", reporter.Dropped())
	}
	available.Store(true)
	for delivered.Load() < 2 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("did not deliver both events: delivered=%d", delivered.Load())
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if reporter.Dropped() != 0 || reporter.Pending() != 0 {
		t.Fatalf("dropped=%d pending=%d", reporter.Dropped(), reporter.Pending())
	}
}

func TestReporterShutdownPersistsQueuedEvents(t *testing.T) {
	spool, err := NewSpool(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	reporter := New("http://127.0.0.1:1", "secret", "dns-01", 4)
	reporter.SetSpool(spool)
	reporter.Enqueue(Event{EventID: "not-persisted"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reporter.Run(ctx)
	if reporter.Pending() != 0 || reporter.Dropped() != 0 || reporter.Delivered() != 0 {
		t.Fatalf("pending=%d dropped=%d delivered=%d", reporter.Pending(), reporter.Dropped(), reporter.Delivered())
	}
	files, err := spool.Files()
	if err != nil || len(files) != 1 {
		t.Fatalf("expected one persisted batch, files=%v err=%v", files, err)
	}
	events, err := spool.Load(files[0])
	if err != nil || len(events) != 1 || events[0].EventID != "not-persisted" {
		t.Fatalf("persisted events=%v err=%v", events, err)
	}
}

func TestReporterPreservesPersistedBatchAcrossUnauthorizedRestart(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
	spool, err := NewSpool(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{EventID: "auth-recovery-id", ObservedAt: time.Now().UTC(), Domain: "example.org", QType: "A", RCode: "NOERROR", ResolutionMode: "normal"}
	if _, err := spool.Save([]Event{event}); err != nil {
		t.Fatal(err)
	}
	var authorized atomic.Bool
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch struct {
			Events []Event `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil || len(batch.Events) != 1 || batch.Events[0].EventID != event.EventID {
			t.Errorf("unexpected event payload: %+v err=%v", batch, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		attempts.Add(1)
		if !authorized.Load() {
			w.WriteHeader(status)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	first := New(server.URL, "secret", "dns-01", 4)
	first.SetSpool(spool)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		first.Run(ctx)
		close(done)
	}()
	deadline := time.After(5 * time.Second)
	for first.FailedBatches() == 0 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("initial authorization failure was not observed")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
	files, err := spool.Files()
	if err != nil || len(files) != 1 || first.Dropped() != 0 {
		t.Fatalf("authorization failure must retain batch: files=%v err=%v dropped=%d", files, err, first.Dropped())
	}
	authorized.Store(true)
	second := New(server.URL, "secret", "dns-01", 4)
	second.SetSpool(spool)
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() {
		second.Run(ctx2)
		close(done2)
	}()
	deadline = time.After(5 * time.Second)
	for second.Delivered() == 0 {
		select {
		case <-deadline:
			cancel2()
			<-done2
			t.Fatal("batch not replayed after authorization recovery")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel2()
	<-done2
	files, err = spool.Files()
	if err != nil || len(files) != 0 || second.Dropped() != 0 || attempts.Load() < 2 {
		t.Fatalf("recovery files=%v err=%v dropped=%d attempts=%d", files, err, second.Dropped(), attempts.Load())
	}
		})
	}
}
