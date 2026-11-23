package publicipsync

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrDataplaneUnverified = errors.New("public IP dataplane state not verified")

// DataplaneVerifier independently inspects the installed network state for
// the authenticated node and exact operation. A received JSON report or
// heartbeat flag is not sufficient evidence of application.
type DataplaneVerifier interface {
	VerifyApplied(ctx context.Context, authenticatedNodeID string, op Operation) (bool, error)
}

// RecordVerified verifies the current operation outside the tracker lock, then
// rechecks the revision/attempt under lock in Record. A concurrent Begin cannot
// cause verification of an older operation to complete a newer revision.
// Node identity MUST come from trusted authentication middleware, never JSON.
func (t *Tracker) RecordVerified(ctx context.Context, authenticatedNodeID string, report NodeReport, verifier DataplaneVerifier) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if verifier == nil || authenticatedNodeID == "" || authenticatedNodeID != report.NodeID || !report.Applied {
		return ErrDataplaneUnverified
	}
	t.mu.Lock()
	p := t.pending[report.SubscriptionID]
	if p == nil {
		t.mu.Unlock()
		return ErrNoPendingOperation
	}
	op := p.op
	t.mu.Unlock()
	if report.SubscriptionID != op.SubscriptionID || report.PublicIPID != op.PublicIPID || report.Revision != op.Revision || report.AttemptID != op.AttemptID {
		return ErrNoPendingOperation
	}
	applied, err := verifier.VerifyApplied(ctx, authenticatedNodeID, op)
	if err != nil {
		return err
	}
	if !applied {
		return ErrDataplaneUnverified
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return t.record(authenticatedNodeID, report)
}

// PendingRevision returns a snapshot for orchestration without exposing mutable
// internal state. It does not imply successful application or durable storage.
func (t *Tracker) PendingRevision(subscriptionID uuid.UUID) (Operation, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := t.pending[subscriptionID]
	if p == nil {
		return Operation{}, false
	}
	return p.op, true
}
