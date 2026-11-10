package queryreport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	mdns "github.com/miekg/dns"
)

type Event struct {
	EventID        string    `json:"event_id"`
	ObservedAt     time.Time `json:"observed_at"`
	Domain         string    `json:"domain"`
	QType          string    `json:"qtype"`
	RCode          string    `json:"rcode"`
	ResolutionMode string    `json:"resolution_mode"`
	DurationMS     int       `json:"duration_ms"`
}

// permanentDeliveryError indicates a request that cannot succeed unchanged.
type permanentDeliveryError struct {
	status int
}

func (e permanentDeliveryError) Error() string {
	return fmt.Sprintf("backend HTTP %d", e.status)
}

type Reporter struct {
	queue         chan Event
	url           string
	key           string
	id            string
	client        *http.Client
	dropped       atomic.Uint64
	delivered     atomic.Uint64
	failedBatches atomic.Uint64
	pendingEvents atomic.Int64
	spool         *Spool
}

func New(url, key, id string, capacity int) *Reporter {
	if capacity <= 0 {
		capacity = 8192
	}
	return &Reporter{
		queue:  make(chan Event, capacity),
		url:    strings.TrimRight(url, "/") + "/api/v1/internal/network/dns-queries/batch",
		key:    key,
		id:     id,
		client: &http.Client{Timeout: 8 * time.Second},
	}
}

// SetSpool enables persistence for batches received by the reporting worker.
// Configure before Run starts. Events still in the channel at a crash are not durable.
func (r *Reporter) SetSpool(spool *Spool) {
	r.spool = spool
}

func (r *Reporter) Enqueue(e Event) {
	r.pendingEvents.Add(1)
	select {
	case r.queue <- e:
	default:
		r.pendingEvents.Add(-1)
		r.dropped.Add(1)
	}
}

func (r *Reporter) Dropped() uint64 {
	return r.dropped.Load()
}

// Delivered returns the number of events acknowledged by Backend.
func (r *Reporter) Delivered() uint64 {
	return r.delivered.Load()
}

// FailedBatches returns the number of failed delivery attempts.
func (r *Reporter) FailedBatches() uint64 {
	return r.failedBatches.Load()
}

// Pending returns events waiting in the channel or active delivery batch.
func (r *Reporter) Pending() int64 {
	return r.pendingEvents.Load()
}

func (r *Reporter) send(ctx context.Context, events []Event) error {
	payload, err := json.Marshal(struct {
		ControllerID string  `json:"controller_id"`
		Events       []Event `json:"events"`
	}{r.id, events})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Network-Control-Key", r.key)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests {
			return permanentDeliveryError{status: resp.StatusCode}
		}
		return fmt.Errorf("backend HTTP %d", resp.StatusCode)
	}
	return nil
}

func (r *Reporter) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	pending := make([]Event, 0, 100)
	var failures uint
	var nextRetry time.Time
	var activeFile string
	var replay []string
	if r.spool != nil {
		files, err := r.spool.Files()
		if err != nil {
			log.Printf("dns query spool listing failed: %v", err)
			return
		}
		replay = files
		log.Printf("dns query spool recovery: %d persisted batches", len(replay))
	}
	flush := func() {
		if time.Now().Before(nextRetry) {
			return
		}
		if len(pending) == 0 && len(replay) > 0 {
			file := replay[0]
			events, err := r.spool.Load(file)
			if err != nil {
				log.Printf("dns query spool unreadable file %s: %v; recovery halted", file, err)
				nextRetry = time.Now().Add(30 * time.Second)
				return
			}
			pending = events
			activeFile = file
			r.pendingEvents.Add(int64(len(events)))
			replay = replay[1:]
		}
		if len(pending) == 0 {
			return
		}
		if r.spool != nil && activeFile == "" {
			file, err := r.spool.Save(pending)
			if err != nil {
				r.failedBatches.Add(1)
				log.Printf("dns query spool save failed: %v", err)
				nextRetry = time.Now().Add(5 * time.Second)
				return
			}
			activeFile = file
		}
		reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := r.send(reqCtx, pending)
		cancel()
		if err == nil {
			if activeFile != "" {
				if err := r.spool.Ack(activeFile); err != nil {
					log.Printf("dns query spool acknowledgement failed: %v; will replay idempotently", err)
					nextRetry = time.Now().Add(5 * time.Second)
					return
				}
			}
			r.delivered.Add(uint64(len(pending)))
			r.pendingEvents.Add(-int64(len(pending)))
			pending = pending[:0]
			activeFile = ""
			failures = 0
			nextRetry = time.Time{}
			return
		}
		r.failedBatches.Add(1)
		var permanent permanentDeliveryError
		if errors.As(err, &permanent) {
			if activeFile != "" {
				if removeErr := r.spool.Ack(activeFile); removeErr != nil {
					log.Printf("dns query spool rejected batch cleanup failed: %v", removeErr)
					nextRetry = time.Now().Add(5 * time.Second)
					return
				}
			}
			r.dropped.Add(uint64(len(pending)))
			r.pendingEvents.Add(-int64(len(pending)))
			log.Printf("dns query reporting rejected batch permanently (HTTP %d, dropped %d)", permanent.status, len(pending))
			pending = pending[:0]
			activeFile = ""
			failures = 0
			nextRetry = time.Time{}
			return
		}
		failures++
		backoff := time.Second
		for i := uint(1); i < failures && i < 6; i++ {
			backoff *= 2
		}
		nextRetry = time.Now().Add(backoff)
		log.Printf("dns query reporting failed (attempt %d, retry in %s, pending %d): %v", failures, backoff, len(pending), err)
	}
	for {
		select {
		case <-ctx.Done():
			remaining := r.pendingEvents.Swap(0)
			if remaining > 0 {
				if r.spool != nil {
					log.Printf("dns query reporting shutdown: %d pending events; persisted batches will replay", remaining)
				} else {
					r.dropped.Add(uint64(remaining))
					log.Printf("dns query reporting shutdown: discarded %d undelivered events", remaining)
				}
			}
			return
		case e := <-r.queue:
			if activeFile != "" {
				// Never append an event to a batch already persisted on disk.
				r.dropped.Add(1)
				r.pendingEvents.Add(-1)
				continue
			}
			if len(pending) >= 100 {
				flush()
			}
			if len(pending) >= 100 {
				r.dropped.Add(1)
				r.pendingEvents.Add(-1)
				continue
			}
			pending = append(pending, e)
			if len(pending) == 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

type Handler struct {
	Next     mdns.Handler
	Reporter *Reporter
}

type writer struct {
	mdns.ResponseWriter
	rcode int
	mode  string
}

func (w *writer) SetResolutionMode(mode string) {
	w.mode = mode
}

func (w *writer) WriteMsg(m *mdns.Msg) error {
	if m != nil {
		w.rcode = m.Rcode
	}
	return w.ResponseWriter.WriteMsg(m)
}

func (h Handler) ServeDNS(w mdns.ResponseWriter, req *mdns.Msg) {
	start := time.Now()
	wrapped := &writer{ResponseWriter: w, rcode: mdns.RcodeServerFailure}
	h.Next.ServeDNS(wrapped, req)
	if req == nil || len(req.Question) == 0 {
		return
	}
	q := req.Question[0]
	domain := strings.ToLower(strings.TrimSuffix(q.Name, "."))
	if domain == "" || len(domain) > 253 {
		return
	}
	qtype := mdns.TypeToString[q.Qtype]
	if qtype == "" {
		qtype = fmt.Sprintf("TYPE%d", q.Qtype)
	}
	rcode := mdns.RcodeToString[wrapped.rcode]
	if rcode == "" {
		rcode = fmt.Sprintf("RCODE%d", wrapped.rcode)
	}
	if wrapped.mode == "" {
		wrapped.mode = "normal"
	}
	elapsed := time.Since(start).Milliseconds()
	if elapsed > 600000 {
		elapsed = 600000
	}
	h.Reporter.Enqueue(Event{
		EventID:        uuid.NewString(),
		ObservedAt:     start.UTC(),
		Domain:         domain,
		QType:          qtype,
		RCode:          rcode,
		ResolutionMode: wrapped.mode,
		DurationMS:     int(elapsed),
	})
}
