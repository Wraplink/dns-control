package publicipsync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

// FileStore persists each subscription as one atomic snapshot. Only one
// FileStore process may own a directory; cross-process locking is not provided.
// Callers must mount dir on persistent storage, not an ephemeral container layer.
type FileStore struct {
	mu sync.Mutex
	dir string
}

func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, ErrDurableStoreUnavailable
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) filename(id uuid.UUID) string {
	return filepath.Join(s.dir, id.String()+".json")
}

func (s *FileStore) read(id uuid.UUID) (DurableSnapshot, error) {
	var snapshot DurableSnapshot
	data, err := os.ReadFile(s.filename(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return snapshot, ErrNoPendingOperation
		}
		return snapshot, err
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return snapshot, err
	}
	if snapshot.Operation.SubscriptionID != id {
		return snapshot, ErrInvalidOperation
	}
	return snapshot, nil
}

func (s *FileStore) write(id uuid.UUID, snapshot DurableSnapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".public-ip-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.filename(id)); err != nil {
		return err
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *FileStore) Begin(ctx context.Context, op Operation, requiredNodes []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := op.Validate(); err != nil {
		return err
	}
	if err := ValidateReports(op, requiredNodes, nil); err != nil && !errors.Is(err, ErrIncomplete) {
		return err
	}
	if len(requiredNodes) == 0 {
		return ErrIncomplete
	}
	seen := map[string]bool{}
	for _, node := range requiredNodes {
		if node == "" || seen[node] {
			return ErrIncomplete
		}
		seen[node] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.read(op.SubscriptionID)
	if err == nil && op.Revision <= old.Operation.Revision {
		return ErrInvalidOperation
	}
	if err != nil && !errors.Is(err, ErrNoPendingOperation) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.write(op.SubscriptionID, DurableSnapshot{Operation: op, RequiredNodes: append([]string(nil), requiredNodes...)})
}

func (s *FileStore) SaveVerified(ctx context.Context, node string, report NodeReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.read(report.SubscriptionID)
	if err != nil {
		return err
	}
	if node == "" || node != report.NodeID || !report.Applied ||
		report.PublicIPID != snapshot.Operation.PublicIPID ||
		report.Revision != snapshot.Operation.Revision ||
		report.AttemptID != snapshot.Operation.AttemptID {
		return ErrNoPendingOperation
	}
	allowed := false
	for _, required := range snapshot.RequiredNodes {
		if required == node {
			allowed = true
		}
	}
	if !allowed {
		return ErrNoPendingOperation
	}
	for _, previous := range snapshot.VerifiedReports {
		if previous.NodeID == node {
			if previous == report {
				return nil
			}
			return ErrIncomplete
		}
	}
	snapshot.VerifiedReports = append(snapshot.VerifiedReports, report)
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.write(report.SubscriptionID, snapshot)
}

func (s *FileStore) Snapshot(ctx context.Context, id uuid.UUID) (DurableSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return DurableSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(id)
}

var _ DurableStore = (*FileStore)(nil)
