package publicipsync

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type verifyFunc func(context.Context, string, Operation) (bool, error)

func (f verifyFunc) VerifyApplied(ctx context.Context, node string, op Operation) (bool, error) {
	return f(ctx, node, op)
}

func TestRecordVerifiedRequiresRealState(t *testing.T) {
	op, nodes, reports := fixture()
	tracker := NewTracker()
	if err := tracker.Begin(op, nodes); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RecordVerified(context.Background(), nodes[0], reports[0], nil); !errors.Is(err, ErrDataplaneUnverified) {
		t.Fatalf("accepted absent verifier: %v", err)
	}
	notApplied := verifyFunc(func(context.Context, string, Operation) (bool, error) { return false, nil })
	if err := tracker.RecordVerified(context.Background(), nodes[0], reports[0], notApplied); !errors.Is(err, ErrDataplaneUnverified) {
		t.Fatalf("accepted unverified dataplane: %v", err)
	}
	if done, err := tracker.Complete(op.SubscriptionID); err != nil || done {
		t.Fatalf("completed without verified report: %v %v", done, err)
	}
	verified := verifyFunc(func(_ context.Context, node string, got Operation) (bool, error) {
		if node != nodes[0] || got != op {
			t.Fatalf("wrong verifier inputs: %s %+v", node, got)
		}
		return true, nil
	})
	if err := tracker.RecordVerified(context.Background(), "spoof", reports[0], verified); !errors.Is(err, ErrDataplaneUnverified) {
		t.Fatalf("accepted spoofed identity: %v", err)
	}
	if err := tracker.RecordVerified(context.Background(), nodes[0], reports[0], verified); err != nil {
		t.Fatal(err)
	}
}

func TestRecordVerifiedConcurrentRevisionChange(t *testing.T) {
	op, nodes, reports := fixture()
	tracker := NewTracker()
	if err := tracker.Begin(op, nodes); err != nil {
		t.Fatal(err)
	}
	newOp := op
	newOp.Revision++
	newOp.AttemptID = uuid.New()
	verifier := verifyFunc(func(context.Context, string, Operation) (bool, error) {
		if err := tracker.Begin(newOp, nodes); err != nil {
			t.Fatal(err)
		}
		return true, nil
	})
	if err := tracker.RecordVerified(context.Background(), nodes[0], reports[0], verifier); !errors.Is(err, ErrNoPendingOperation) {
		t.Fatalf("accepted stale verification: %v", err)
	}
	if done, err := tracker.Complete(op.SubscriptionID); err != nil || done {
		t.Fatalf("completed newer revision with stale report: %v %v", done, err)
	}
}

func TestRecordVerifiedRejectsCancellationDuringVerification(t *testing.T) {
	op, nodes, reports := fixture()
	tracker := NewTracker()
	if err := tracker.Begin(op, nodes); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	verifier := verifyFunc(func(context.Context, string, Operation) (bool, error) {
		cancel()
		return true, nil
	})
	if err := tracker.RecordVerified(ctx, nodes[0], reports[0], verifier); !errors.Is(err, context.Canceled) {
		t.Fatalf("accepted canceled verification: %v", err)
	}
	if done, err := tracker.Complete(op.SubscriptionID); err != nil || done {
		t.Fatalf("completed canceled verification: %v %v", done, err)
	}
}
