package publicipsync

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrDurableStoreUnavailable = errors.New("durable public IP acknowledgement store unavailable")

// DurableStore is the required transactional persistence boundary for public IP
// activation. Implementations must commit operation revisions, required node
// snapshots, and verified reports atomically, with restart-safe recovery.
//
// Begin must reject stale revisions and preserve immutable required nodes.
// SaveVerified must compare the entire operation and authenticated node ID
// against the committed snapshot in one transaction; a newer Begin wins.
// Snapshot must read only committed state. No successful call may be served
// from an in-memory cache before the transaction is committed.
type DurableStore interface {
	Begin(ctx context.Context, op Operation, requiredNodes []string) error
	SaveVerified(ctx context.Context, authenticatedNodeID string, report NodeReport) error
	Snapshot(ctx context.Context, subscriptionID uuid.UUID) (DurableSnapshot, error)
}

// DurableSnapshot is the committed state needed to reconstruct completion
// after a process restart. Never infer completion from report count alone.
type DurableSnapshot struct {
	Operation Operation
	RequiredNodes []string
	VerifiedReports []NodeReport
}

// Complete validates exact identity and revision matches for every required
// node, rather than trusting a persisted completion boolean.
func (s DurableSnapshot) Complete() (bool, error) {
	if err := s.Operation.Validate(); err != nil {
		return false, err
	}
	if err := ValidateReports(s.Operation, s.RequiredNodes, s.VerifiedReports); err != nil {
		if errors.Is(err, ErrIncomplete) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
