package publicipsync

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestTrackerRequiresAllNodes(t *testing.T) {
	op, nodes, reports := fixture()
	tracker := NewTracker()
	if err := tracker.Begin(op, nodes); err != nil {
		t.Fatal(err)
	}
	if done, err := tracker.Complete(op.SubscriptionID); err != nil || done {
		t.Fatalf("completed without reports: %v %v", done, err)
	}
	if err := tracker.record(nodes[0], reports[0]); err != nil {
		t.Fatal(err)
	}
	if done, err := tracker.Complete(op.SubscriptionID); err != nil || done {
		t.Fatalf("completed with one report: %v %v", done, err)
	}
	if err := tracker.record(nodes[1], reports[1]); err != nil {
		t.Fatal(err)
	}
	if done, err := tracker.Complete(op.SubscriptionID); err != nil || !done {
		t.Fatalf("did not complete with both reports: %v %v", done, err)
	}
}

func TestTrackerRejectsSpoofedAndStaleReports(t *testing.T) {
	op, nodes, reports := fixture()
	tracker := NewTracker()
	if err := tracker.Begin(op, nodes); err != nil {
		t.Fatal(err)
	}
	if err := tracker.record("different-node", reports[0]); !errors.Is(err, ErrNoPendingOperation) {
		t.Fatalf("accepted spoofed identity: %v", err)
	}
	stale := reports[0]
	stale.Revision--
	if err := tracker.record(nodes[0], stale); !errors.Is(err, ErrNoPendingOperation) {
		t.Fatalf("accepted stale revision: %v", err)
	}
	if err := tracker.record(nodes[0], reports[0]); err != nil {
		t.Fatal(err)
	}
	newOp := op
	newOp.Revision++
	newOp.AttemptID = uuid.New()
	if err := tracker.Begin(newOp, nodes); err != nil {
		t.Fatal(err)
	}
	if err := tracker.record(nodes[0], reports[0]); !errors.Is(err, ErrNoPendingOperation) {
		t.Fatalf("accepted prior attempt: %v", err)
	}
	if done, err := tracker.Complete(op.SubscriptionID); err != nil || done {
		t.Fatalf("old report survived new revision: %v %v", done, err)
	}
	if err := tracker.Begin(op, nodes); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("accepted rollback: %v", err)
	}
	if err := tracker.Begin(newOp, nodes); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("accepted same-revision attempt replacement: %v", err)
	}
}
