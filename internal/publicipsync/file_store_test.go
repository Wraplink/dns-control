package publicipsync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestFileStoreRestartAndRevisionFence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	op, nodes, reports := fixture()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Begin(ctx, op, nodes); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveVerified(ctx, nodes[0], reports[0]); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := restarted.Snapshot(ctx, op.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if complete, err := snapshot.Complete(); err != nil || complete {
		t.Fatalf("partial restart snapshot: complete=%v err=%v", complete, err)
	}
	if err := restarted.SaveVerified(ctx, nodes[1], reports[1]); err != nil {
		t.Fatal(err)
	}
	snapshot, err = restarted.Snapshot(ctx, op.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if complete, err := snapshot.Complete(); err != nil || !complete {
		t.Fatalf("full restart snapshot: complete=%v err=%v", complete, err)
	}
	if err := restarted.Begin(ctx, op, nodes); err != nil {
		t.Fatalf("identical revision retry rejected: %v", err)
	}
	next := op
	next.Revision++
	if err := restarted.Begin(ctx, next, nodes); err != nil {
		t.Fatal(err)
	}
	if err := restarted.SaveVerified(ctx, nodes[0], reports[0]); !errors.Is(err, ErrNoPendingOperation) {
		t.Fatalf("accepted stale report: %v", err)
	}
	snapshot, err = restarted.Snapshot(ctx, op.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if complete, err := snapshot.Complete(); err != nil || complete {
		t.Fatalf("new revision retained old completion: complete=%v err=%v", complete, err)
	}
}

func TestFileStoreConcurrentInstancesPreserveReports(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	op, nodes, reports := fixture()
	first, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Begin(ctx, op, nodes); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, store := range []*FileStore{first, second} {
		wg.Add(1)
		go func(i int, store *FileStore) {
			defer wg.Done()
			errs <- store.SaveVerified(ctx, nodes[i], reports[i])
		}(i, store)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := restarted.Snapshot(ctx, op.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if complete, err := snapshot.Complete(); err != nil || !complete {
		t.Fatalf("concurrent reports lost: complete=%v err=%v", complete, err)
	}
}

func TestFileStoreConcurrentRevisionFencesStaleReport(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	op, nodes, reports := fixture()
	first, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Begin(ctx, op, nodes[:1]); err != nil {
		t.Fatal(err)
	}
	next := op
	next.Revision++
	next.AttemptID = uuid.New()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = first.SaveVerified(ctx, nodes[0], reports[0])
	}()
	go func() {
		defer wg.Done()
		if err := second.Begin(ctx, next, nodes[:1]); err != nil {
			t.Errorf("begin next revision: %v", err)
		}
	}()
	wg.Wait()
	snapshot, err := first.Snapshot(ctx, op.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Operation.Revision != next.Revision || len(snapshot.VerifiedReports) != 0 {
		t.Fatalf("new revision retained stale acknowledgement: %+v", snapshot)
	}
}

func TestFileStoreBeginRetryPreservesVerifiedReports(t *testing.T) {
	ctx := context.Background()
	op, nodes, reports := fixture()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Begin(ctx, op, nodes); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveVerified(ctx, nodes[0], reports[0]); err != nil {
		t.Fatal(err)
	}
	if err := store.Begin(ctx, op, nodes); err != nil {
		t.Fatalf("identical retry rejected: %v", err)
	}
	snapshot, err := store.Snapshot(ctx, op.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.VerifiedReports) != 1 || snapshot.VerifiedReports[0] != reports[0] {
		t.Fatalf("retry erased acknowledged node: %+v", snapshot.VerifiedReports)
	}
	replaced := op
	replaced.AttemptID = uuid.New()
	if err := store.Begin(ctx, replaced, nodes); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("same revision with changed attempt accepted: %v", err)
	}
}

func TestFileStoreRejectsDuplicatePersistedReports(t *testing.T) {
	ctx := context.Background()
	op, nodes, reports := fixture()
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Begin(ctx, op, nodes); err != nil {
		t.Fatal(err)
	}
	snapshot := DurableSnapshot{
		Operation:       op,
		RequiredNodes:   nodes,
		VerifiedReports: []NodeReport{reports[0], reports[0]},
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, op.SubscriptionID.String()+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Snapshot(ctx, op.SubscriptionID); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("duplicate persisted report accepted: %v", err)
	}
}

func TestFileStoreRejectsWritableDirectoryPermissions(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(dir); err != nil {
		t.Fatalf("store failed to enforce private permissions: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("store directory permissions = %o, want 0700", info.Mode().Perm())
	}
}
