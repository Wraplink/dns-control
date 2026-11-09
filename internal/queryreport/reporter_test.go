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
func (w *responseWriter) TsigTimersOnly(bool)      {}
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
