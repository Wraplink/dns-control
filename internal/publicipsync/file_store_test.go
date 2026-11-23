package publicipsync

import (
	"context"
	"errors"
	"sync"
	"testing"
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
	if err := restarted.Begin(ctx, op, nodes); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("accepted repeated revision: %v", err)
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
