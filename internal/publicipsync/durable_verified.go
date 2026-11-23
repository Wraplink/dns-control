package publicipsync

import "context"

// SaveDataplaneVerified checks the committed revision, independently verifies
// the actual dataplane, and atomically records the acknowledgement. The store
// must compare the revision again during SaveVerified to fence concurrent Begin.
func SaveDataplaneVerified(ctx context.Context, store DurableStore, node AuthenticatedNode, report NodeReport, verifier DataplaneVerifier) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil {
		return ErrDurableStoreUnavailable
	}
	if verifier == nil || node.ID == "" || node.ID != report.NodeID || !report.Applied {
		return ErrDataplaneUnverified
	}
	snapshot, err := store.Snapshot(ctx, report.SubscriptionID)
	if err != nil {
		return err
	}
	op := snapshot.Operation
	if op.SubscriptionID != report.SubscriptionID || op.PublicIPID != report.PublicIPID || op.Revision != report.Revision || op.AttemptID != report.AttemptID {
		return ErrNoPendingOperation
	}
	allowed := false
	for _, required := range snapshot.RequiredNodes {
		if required == node.ID {
			allowed = true
			break
		}
	}
	if !allowed {
		return ErrNoPendingOperation
	}
	applied, err := verifier.VerifyApplied(ctx, node.ID, op)
	if err != nil {
		return err
	}
	if !applied {
		return ErrDataplaneUnverified
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return store.SaveVerified(ctx, node.ID, report)
}
