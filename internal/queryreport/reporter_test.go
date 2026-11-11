package queryreport

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	for !delivered.Load() || reporter.Delivered() != 1 {
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

func TestHandlerReportsRootZoneQuery(t *testing.T) {
	reporter := New("http://127.0.0.1:1", "secret", "dns-01", 2)
	handler := Handler{
		Reporter: reporter,
		Next: mdns.HandlerFunc(func(w mdns.ResponseWriter, req *mdns.Msg) {
			reply := new(mdns.Msg)
			reply.SetReply(req)
			if err := w.WriteMsg(reply); err != nil {
				t.Errorf("write root-zone DNS response: %v", err)
			}
		}),
	}
	request := new(mdns.Msg)
	request.SetQuestion(".", mdns.TypeNS)
	handler.ServeDNS(&responseWriter{}, request)
	select {
	case event := <-reporter.queue:
		if event.Domain != "." || event.QType != "NS" || event.RCode != "NOERROR" {
			t.Fatalf("unexpected root-zone DNS audit event: %+v", event)
		}
	default:
		t.Fatal("root-zone DNS query was not reported")
	}
}

func TestHandlerReportsDNSServiceLabels(t *testing.T) {
	for _, name := range []string{"_sip._tcp.example.org.", "_dmarc.example.org.", "_acme-challenge.example.org."} {
		t.Run(name, func(t *testing.T) {
			reporter := New("http://127.0.0.1:1", "secret", "dns-01", 2)
			handler := Handler{
				Reporter: reporter,
				Next: mdns.HandlerFunc(func(w mdns.ResponseWriter, req *mdns.Msg) {
					reply := new(mdns.Msg)
					reply.SetReply(req)
					if err := w.WriteMsg(reply); err != nil {
						t.Errorf("write DNS response: %v", err)
					}
				}),
			}
			request := new(mdns.Msg)
			request.SetQuestion(name, mdns.TypeTXT)
			handler.ServeDNS(&responseWriter{}, request)
			select {
			case event := <-reporter.queue:
				if event.Domain != strings.TrimSuffix(name, ".") || event.QType != "TXT" {
					t.Fatalf("unexpected DNS service audit event: %+v", event)
				}
			default:
				t.Fatal("DNS service-label query was not reported")
			}
		})
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
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			var wrongID atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var batch struct {
					Events []Event `json:"events"`
				}
				if err := json.NewDecoder(r.Body).Decode(&batch); err != nil || len(batch.Events) != 1 || batch.Events[0].EventID != "retry-id" {
					wrongID.Store(true)
				}
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
			if calls.Load() != 2 || wrongID.Load() || reporter.Dropped() != 0 || reporter.FailedBatches() != 1 || reporter.Pending() != 0 {
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

func TestReporterShutdownAccountsForUndeliveredEvents(t *testing.T) {
	reporter := New("http://127.0.0.1:1", "secret", "dns-01", 4)
	reporter.Enqueue(Event{EventID: "one"})
	reporter.Enqueue(Event{EventID: "two"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reporter.Run(ctx)
	if reporter.Pending() != 0 || reporter.Dropped() != 2 || reporter.Delivered() != 0 {
		t.Fatalf("pending=%d dropped=%d delivered=%d", reporter.Pending(), reporter.Dropped(), reporter.Delivered())
	}
}

func TestReporterRejectsEventsAfterShutdown(t *testing.T) {
	reporter := New("http://127.0.0.1:1", "secret", "dns-test", 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reporter.Run(ctx)
		close(done)
	}()
	cancel()
	<-done
	reporter.Enqueue(Event{EventID: "after-stop"})
	if reporter.Pending() != 0 {
		t.Fatalf("pending after shutdown = %d, want 0", reporter.Pending())
	}
	if reporter.Dropped() != 1 {
		t.Fatalf("dropped after shutdown = %d, want 1", reporter.Dropped())
	}
}

func TestReporterConcurrentEnqueueDuringShutdown(t *testing.T) {
	const producers = 8
	const eventsPerProducer = 100
	reporter := New("http://127.0.0.1:1", "secret", "dns-test", 64)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reporter.Run(ctx)
		close(done)
	}()
	var wg sync.WaitGroup
	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < eventsPerProducer; j++ {
				reporter.Enqueue(Event{EventID: "concurrent"})
			}
		}()
	}
	cancel()
	wg.Wait()
	<-done
	if got := reporter.Pending(); got != 0 {
		t.Fatalf("pending after concurrent shutdown = %d, want 0", got)
	}
	if got := reporter.Delivered() + reporter.Dropped(); got != producers*eventsPerProducer {
		t.Fatalf("accounted events = %d, want %d", got, producers*eventsPerProducer)
	}
}

func TestDNSHandlerToBackendBatchIntegration(t *testing.T) {
	received := make(chan Event, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/internal/network/dns-queries/batch" {
			t.Errorf("unexpected ingestion request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("X-Network-Control-Key") != "integration-secret" {
			t.Error("missing network control key")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var payload struct {
			ControllerID string  `json:"controller_id"`
			Events       []Event `json:"events"`
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.ControllerID != "dns-integration" || len(payload.Events) != 1 {
			t.Errorf("unexpected payload: %+v", payload)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var raw struct {
			Events []map[string]json.RawMessage `json:"events"`
		}
		if err := json.Unmarshal(body, &raw); err != nil || len(raw.Events) != 1 {
			t.Errorf("decode raw request fields: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		eventFields := raw.Events[0]
		for _, forbidden := range []string{"client_ip", "source_ip", "remote_addr"} {
			if _, exists := eventFields[forbidden]; exists {
				t.Errorf("unexpected client address field %q", forbidden)
			}
		}
		received <- payload.Events[0]
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	spool, err := NewSpool(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	reporter := New(server.URL, "integration-secret", "dns-integration", 8)
	reporter.SetSpool(spool)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		reporter.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	handler := Handler{
		Reporter: reporter,
		Next: mdns.HandlerFunc(func(w mdns.ResponseWriter, req *mdns.Msg) {
			if setter, ok := w.(interface{ SetResolutionMode(string) }); ok {
				setter.SetResolutionMode("route")
			}
			reply := new(mdns.Msg)
			reply.SetReply(req)
			if err := w.WriteMsg(reply); err != nil {
				t.Errorf("DNS response: %v", err)
			}
		}),
	}
	query := new(mdns.Msg)
	query.SetQuestion("Managed.Example.", mdns.TypeA)
	writer := &responseWriter{}
	handler.ServeDNS(writer, query)
	if writer.msg == nil || writer.msg.Rcode != mdns.RcodeSuccess {
		t.Fatalf("DNS query failed: %+v", writer.msg)
	}
	select {
	case event := <-received:
		if event.EventID == "" || event.Domain != "managed.example" || event.QType != "A" || event.RCode != "NOERROR" || event.ResolutionMode != "route" || event.ObservedAt.IsZero() {
			t.Fatalf("unexpected DNS query event: %+v", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DNS query event was not ingested")
	}
	deadline := time.After(5 * time.Second)
	for {
		files, err := spool.Files()
		if err != nil {
			t.Fatal(err)
		}
		if reporter.Delivered() == 1 && reporter.Pending() == 0 && len(files) == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("ingested event was not acknowledged: delivered=%d pending=%d spool_files=%d", reporter.Delivered(), reporter.Pending(), len(files))
		case <-time.After(20 * time.Millisecond):
		}
	}
}
