package publicipsync

import (
	"errors"
	"sync"

	"github.com/google/uuid"
)

var ErrNoPendingOperation = errors.New("no matching pending public IP operation")

// Tracker is an in-memory synchronization primitive for tests and orchestration.
// It is NOT a durable source of truth: production must persist operations,
// required-node snapshots and authenticated acknowledgements transactionally.
type Tracker struct {
	mu      sync.Mutex
	pending map[uuid.UUID]*pendingOperation
}

type pendingOperation struct {
	op       Operation
	required []string
	reports  map[string]NodeReport
}

func NewTracker() *Tracker {
	return &Tracker{pending: make(map[uuid.UUID]*pendingOperation)}
}

// Begin replaces a pending attempt only with a strictly newer revision.
// A retry of the same revision must retain its attempt ID and node snapshot.
func (t *Tracker) Begin(op Operation, required []string) error {
	if err := op.Validate(); err != nil {
		return err
	}
	if len(required) == 0 {
		return ErrIncomplete
	}
	seen := make(map[string]bool, len(required))
	for _, node := range required {
		if node == "" || seen[node] {
			return ErrIncomplete
		}
		seen[node] = true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == nil {
		t.pending = make(map[uuid.UUID]*pendingOperation)
	}
	if old := t.pending[op.SubscriptionID]; old != nil && op.Revision <= old.op.Revision {
		return ErrInvalidOperation
	}
	t.pending[op.SubscriptionID] = &pendingOperation{op: op, required: append([]string(nil), required...), reports: make(map[string]NodeReport)}
	return nil
}

// Record accepts reports only after the caller authenticates the node identity
// and verifies the installed dataplane state. Never trust NodeID from JSON.
func (t *Tracker) Record(authenticatedNodeID string, report NodeReport) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := t.pending[report.SubscriptionID]
	if p == nil || authenticatedNodeID == "" || report.NodeID != authenticatedNodeID {
		return ErrNoPendingOperation
	}
	found := false
	for _, node := range p.required {
		if node == authenticatedNodeID {
			found = true
			break
		}
	}
	if !found || !report.Applied || report.SubscriptionID != p.op.SubscriptionID || report.PublicIPID != p.op.PublicIPID || report.Revision != p.op.Revision || report.AttemptID != p.op.AttemptID {
		return ErrNoPendingOperation
	}
	if existing, ok := p.reports[authenticatedNodeID]; ok && existing != report {
		return ErrIncomplete
	}
	p.reports[authenticatedNodeID] = report
	return nil
}

func (t *Tracker) Complete(subscriptionID uuid.UUID) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := t.pending[subscriptionID]
	if p == nil {
		return false, ErrNoPendingOperation
	}
	reports := make([]NodeReport, 0, len(p.reports))
	for _, report := range p.reports {
		reports = append(reports, report)
	}
	if err := ValidateReports(p.op, p.required, reports); err != nil {
		if errors.Is(err, ErrIncomplete) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
