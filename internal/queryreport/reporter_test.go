package queryreport

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
)

func TestReporterSendsBatchWithControlKey(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/internal/network/dns-queries/batch" || r.Header.Get("X-Network-Control-Key") != "secret" {
			t.Errorf("unexpected request path or control key")
		}
		var batch struct {
			ControllerID string  `json:"controller_id"`
			Events       []Event `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Errorf("decode batch: %v", err)
		}
		if batch.ControllerID != "dns-01" || len(batch.Events) != 1 {
			t.Errorf("unexpected batch: %+v", batch)
		}
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	reporter := New(server.URL, "secret", "dns-01", 4)
	event := Event{EventID: "test-id", ObservedAt: time.Now().UTC(), Domain: "example.org", QType: "A", RCode: "NOERROR", ResolutionMode: "observed"}
	reporter.Enqueue(event)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reporter.Run(ctx)
		close(done)
	}()
	deadline := time.After(3 * time.Second)
	for calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("batch not sent")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if reporter.Dropped() != 0 {
		t.Fatalf("unexpected dropped events: %d", reporter.Dropped())
	}
}

func TestQueueOverflowDoesNotBlock(t *testing.T) {
	reporter := New("http://127.0.0.1:1", "secret", "dns-01", 1)
	reporter.Enqueue(Event{})
	reporter.Enqueue(Event{})
	if reporter.Dropped() != 1 {
		t.Fatalf("expected one dropped event, got %d", reporter.Dropped())
	}
}

type responseWriter struct {
	msg *mdns.Msg
}

func (w *responseWriter) LocalAddr() net.Addr  { return &net.UDPAddr{} }
func (w *responseWriter) RemoteAddr() net.Addr { return &net.UDPAddr{} }
func (w *responseWriter) WriteMsg(msg *mdns.Msg) error {
	w.msg = msg
	return nil
}
func (w *responseWriter) Write([]byte) (int, error) { return 0, nil }
func (w *responseWriter) Close() error              { return nil }
func (w *responseWriter) TsigStatus() error         { return nil }
func (w *responseWriter) TsigTimersOnly(bool)       {}
func (w *responseWriter) Hijack()                   {}

func TestHandlerCapturesRefused(t *testing.T) {
	reporter := New("http://127.0.0.1:1", "secret", "dns-01", 2)
	h := Handler{Reporter: reporter, Next: mdns.HandlerFunc(func(w mdns.ResponseWriter, req *mdns.Msg) {
		msg := new(mdns.Msg)
		msg.SetRcode(req, mdns.RcodeRefused)
		_ = w.WriteMsg(msg)
	})}
	req := new(mdns.Msg)
	req.SetQuestion("Example.ORG.", mdns.TypeAAAA)
	h.ServeDNS(&responseWriter{}, req)
	select {
	case event := <-reporter.queue:
		if event.Domain != "example.org" || event.QType != "AAAA" || event.RCode != "REFUSED" {
			t.Fatalf("unexpected event: %+v", event)
		}
	default:
		t.Fatal("no query event recorded")
	}
}

func TestReporterRetriesFailedBatchWithSameEventID(t *testing.T) {
	var calls atomic.Int32
	var firstID string
	var delivered atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch struct {
			Events []Event `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Errorf("decode: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(batch.Events) != 1 {
			t.Errorf("expected one event, got %d", len(batch.Events))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n := calls.Add(1)
		if n == 1 {
			firstID = batch.Events[0].EventID
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if batch.Events[0].EventID != firstID {
			t.Errorf("retry changed event ID: %s -> %s", firstID, batch.Events[0].EventID)
		}
		delivered.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	reporter := New(server.URL, "secret", "dns-01", 4)
	reporter.Enqueue(Event{
		EventID: "stable-event-id", ObservedAt: time.Now().UTC(),
		Domain: "example.org", QType: "A", RCode: "NOERROR",
		ResolutionMode: "observed",
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reporter.Run(ctx)
		close(done)
	}()
	deadline := time.After(6 * time.Second)
	for !delivered.Load() {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("reporter did not recover from transient backend failure")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if calls.Load() < 2 || reporter.Dropped() != 0 || reporter.Delivered() != 1 || reporter.FailedBatches() != 1 || reporter.Pending() != 0 {
		t.Fatalf("calls=%d dropped=%d delivered=%d failed=%d", calls.Load(), reporter.Dropped(), reporter.Delivered(), reporter.FailedBatches())
	}
}

func TestHandlerCapturesSelectedResolutionMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want string
	}{
		{name: "managed route", mode: "route", want: "route"},
		{name: "managed hijack", mode: "hijack", want: "hijack"},
		{name: "ordinary upstream", mode: "normal", want: "normal"},
		{name: "default ordinary", mode: "", want: "normal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reporter := New("http://127.0.0.1:1", "secret", "dns-01", 2)
			h := Handler{Reporter: reporter, Next: mdns.HandlerFunc(func(w mdns.ResponseWriter, req *mdns.Msg) {
				if tt.mode != "" {
					if setter, ok := w.(interface{ SetResolutionMode(string) }); ok {
						setter.SetResolutionMode(tt.mode)
					}
				}
				msg := new(mdns.Msg)
				msg.SetReply(req)
				_ = w.WriteMsg(msg)
			})}
			req := new(mdns.Msg)
			req.SetQuestion("example.org.", mdns.TypeA)
			h.ServeDNS(&responseWriter{}, req)
			select {
			case event := <-reporter.queue:
				if event.ResolutionMode != tt.want {
					t.Fatalf("mode=%q, want %q", event.ResolutionMode, tt.want)
				}
			default:
				t.Fatal("no event")
			}
		})
	}
}

func TestReporterPermanentRejectionDropsBatchAndContinues(t *testing.T) {
	var calls atomic.Int32
	var secondDelivered atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch struct {
			Events []Event `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Errorf("decode: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls.Add(1)
		if len(batch.Events) != 1 {
			t.Errorf("unexpected batch size: %d", len(batch.Events))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if batch.Events[0].EventID == "invalid" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if batch.Events[0].EventID == "valid" {
			secondDelivered.Store(true)
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Errorf("unexpected event ID %q", batch.Events[0].EventID)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	reporter := New(server.URL, "secret", "dns-01", 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reporter.Run(ctx)
		close(done)
	}()
	reporter.Enqueue(Event{EventID: "invalid", ObservedAt: time.Now().UTC(), Domain: "example.org", QType: "A", RCode: "NOERROR", ResolutionMode: "normal"})
	deadline := time.After(6 * time.Second)
	for reporter.Dropped() == 0 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("permanent rejection did not discard the batch")
		case <-time.After(20 * time.Millisecond):
		}
	}
	reporter.Enqueue(Event{EventID: "valid", ObservedAt: time.Now().UTC(), Domain: "example.org", QType: "A", RCode: "NOERROR", ResolutionMode: "normal"})
	for !secondDelivered.Load() {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("subsequent batch not delivered after rejection")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if reporter.Dropped() != 1 || reporter.Delivered() != 1 || reporter.FailedBatches() != 1 || reporter.Pending() != 0 || calls.Load() != 2 {
		t.Fatalf("dropped=%d delivered=%d failed=%d calls=%d", reporter.Dropped(), reporter.Delivered(), reporter.FailedBatches(), calls.Load())
	}
}

func TestReporterRetryableHTTPStatuses(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			reporter := New(server.URL, "secret", "dns-01", 4)
			event := Event{EventID: "retry-id", ObservedAt: time.Now().UTC(), Domain: "example.org", QType: "A", RCode: "NOERROR", ResolutionMode: "normal"}
			reporter.Enqueue(event)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				reporter.Run(ctx)
				close(done)
			}()
			deadline := time.After(6 * time.Second)
			for reporter.Delivered() == 0 {
				select {
				case <-deadline:
					cancel()
					<-done
					t.Fatalf("status %d was not retried", status)
				case <-time.After(20 * time.Millisecond):
				}
			}
			cancel()
			<-done
			if calls.Load() != 2 || reporter.Dropped() != 0 || reporter.FailedBatches() != 1 {
				t.Fatalf("calls=%d dropped=%d failed=%d", calls.Load(), reporter.Dropped(), reporter.FailedBatches())
			}
		})
	}
}

func TestPendingCountsQueuedAndDroppedEvents(t *testing.T) {
	reporter := New("http://127.0.0.1:1", "secret", "dns-01", 1)
	reporter.Enqueue(Event{EventID: "queued"})
	if reporter.Pending() != 1 || reporter.Dropped() != 0 {
		t.Fatalf("pending=%d dropped=%d", reporter.Pending(), reporter.Dropped())
	}
	reporter.Enqueue(Event{EventID: "overflow"})
	if reporter.Pending() != 1 || reporter.Dropped() != 1 {
		t.Fatalf("pending=%d dropped=%d", reporter.Pending(), reporter.Dropped())
	}
}
