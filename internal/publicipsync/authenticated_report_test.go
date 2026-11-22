package publicipsync

import (
	"context"
	"errors"
	"testing"
)

func TestAuthenticatedReportBoundary(t *testing.T) {
	op, nodes, reports := fixture()
	tracker := NewTracker()
	if err := tracker.Begin(op, nodes); err != nil {
		t.Fatal(err)
	}
	calls := 0
	verifier := verifyFunc(func(_ context.Context, node string, got Operation) (bool, error) {
		calls++
		if node != nodes[0] || got != op {
			t.Fatalf("incorrect trusted verification inputs: %q %+v", node, got)
		}
		return true, nil
	})
	if err := tracker.ReportFromAuthenticatedNode(context.Background(), AuthenticatedNode{}, reports[0], verifier); !errors.Is(err, ErrUnauthenticatedNode) {
		t.Fatalf("accepted missing identity: %v", err)
	}
	if err := tracker.ReportFromAuthenticatedNode(context.Background(), AuthenticatedNode{ID: nodes[1]}, reports[0], verifier); !errors.Is(err, ErrUnauthenticatedNode) {
		t.Fatalf("accepted forged identity: %v", err)
	}
	if calls != 0 {
		t.Fatalf("verifier called for forged identity: %d", calls)
	}
	report := reports[0]
	report.NodeID = ""
	if err := tracker.ReportFromAuthenticatedNode(context.Background(), AuthenticatedNode{ID: nodes[0]}, report, verifier); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("verification calls = %d", calls)
	}
}
