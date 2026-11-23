package publicipsync

import (
	"testing"

	"github.com/google/uuid"
)

func TestDurableSnapshotCompletion(t *testing.T) {
	op, nodes, reports := fixture()
	snapshot := DurableSnapshot{Operation: op, RequiredNodes: nodes, VerifiedReports: reports[:1]}
	if complete, err := snapshot.Complete(); err != nil || complete {
		t.Fatalf("partial snapshot completed: complete=%v err=%v", complete, err)
	}
	snapshot.VerifiedReports = reports
	if complete, err := snapshot.Complete(); err != nil || !complete {
		t.Fatalf("fully verified snapshot incomplete: complete=%v err=%v", complete, err)
	}
	stale := append([]NodeReport(nil), reports...)
	stale[0].Revision--
	snapshot.VerifiedReports = stale
	if complete, err := snapshot.Complete(); err != nil || complete {
		t.Fatalf("stale snapshot completed: complete=%v err=%v", complete, err)
	}
	snapshot.VerifiedReports = reports
	snapshot.Operation.AttemptID = uuid.New()
	if complete, err := snapshot.Complete(); err != nil || complete {
		t.Fatalf("wrong attempt completed: complete=%v err=%v", complete, err)
	}
}
