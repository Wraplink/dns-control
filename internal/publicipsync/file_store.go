package publicipsync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"

	"github.com/google/uuid"
)

// FileStore persists each subscription as one atomic snapshot. Advisory
// directory locking serializes cooperating local processes on Linux.
// Callers must mount dir on persistent local storage; network filesystems and
// non-cooperating writers are not supported.
type FileStore struct {
	mu  sync.Mutex
	dir string
}

func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, ErrDurableStoreUnavailable
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// Inspect the path before chmod: chmod follows symlinks and must not
	// modify permissions on an unrelated target directory.
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrDurableStoreUnavailable
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	info, err = os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return nil, ErrDurableStoreUnavailable
	}
	return &FileStore{dir: dir}, nil
}

// lock acquires a process-shared advisory lock before reading or writing.
// The lock file must never be deleted while the store is in use.
func (s *FileStore) lock() (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(s.dir, ".store.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func unlock(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
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
		return DurableSnapshot{}, ErrInvalidOperation
	}
	if err := snapshot.Operation.Validate(); err != nil {
		return DurableSnapshot{}, err
	}
	if len(snapshot.RequiredNodes) == 0 {
		return DurableSnapshot{}, ErrInvalidOperation
	}
	seen := make(map[string]bool, len(snapshot.RequiredNodes))
	for _, node := range snapshot.RequiredNodes {
		if node == "" || seen[node] {
			return DurableSnapshot{}, ErrInvalidOperation
		}
		seen[node] = true
	}
	reported := make(map[string]bool, len(snapshot.VerifiedReports))
	for _, report := range snapshot.VerifiedReports {
		if reported[report.NodeID] {
			return DurableSnapshot{}, ErrInvalidOperation
		}
		reported[report.NodeID] = true
		if !seen[report.NodeID] || !report.Applied || report.SubscriptionID != id || report.PublicIPID != snapshot.Operation.PublicIPID || report.Revision != snapshot.Operation.Revision || report.AttemptID != snapshot.Operation.AttemptID {
			return DurableSnapshot{}, ErrInvalidOperation
		}
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
	lock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock(lock)
	old, err := s.read(op.SubscriptionID)
	if err == nil && op.Revision <= old.Operation.Revision {
		if op == old.Operation && slices.Equal(requiredNodes, old.RequiredNodes) {
			return nil
		}
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
	lock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock(lock)
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
	lock, err := s.lock()
	if err != nil {
		return DurableSnapshot{}, err
	}
	defer unlock(lock)
	return s.read(id)
}

var _ DurableStore = (*FileStore)(nil)
