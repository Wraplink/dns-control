package queryreport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrSpoolFull = errors.New("dns query spool capacity exceeded")

// Spool persists batches before transmission. Acknowledged batches are removed.
// A replay can contain already accepted events; Backend deduplicates by event ID.
type Spool struct {
	mu       sync.Mutex
	dir      string
	maxBytes int64
}

func NewSpool(dir string, maxBytes int64) (*Spool, error) {
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &Spool{dir: dir, maxBytes: maxBytes}, nil
}

// CleanupStaleTemps removes only incomplete temporary writes older than maxAge.
// It must run before the reporting worker starts writing new batches.
func (s *Spool) CleanupStaleTemps(maxAge time.Duration) (int, error) {
	if maxAge <= 0 {
		return 0, errors.New("invalid temporary file maximum age")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return removed, err
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
		removed++
	}
	if removed > 0 {
		if err := syncDir(s.dir); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

func (s *Spool) Files() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, filepath.Join(s.dir, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func (s *Spool) Save(events []Event) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(events) == 0 {
		return "", errors.New("empty DNS query batch")
	}
	payload, err := json.Marshal(events)
	if err != nil {
		return "", err
	}
	files, err := s.Files()
	if err != nil {
		return "", err
	}
	var used int64
	for _, file := range files {
		info, err := os.Lstat(file)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		used += info.Size()
	}
	if int64(len(payload)) > s.maxBytes-used {
		return "", ErrSpoolFull
	}
	temp, err := os.CreateTemp(s.dir, ".pending-")
	if err != nil {
		return "", err
	}
	defer os.Remove(temp.Name())
	if err = temp.Chmod(0600); err != nil {
		temp.Close()
		return "", err
	}
	if _, err = temp.Write(payload); err != nil {
		temp.Close()
		return "", err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return "", err
	}
	if err = temp.Close(); err != nil {
		return "", err
	}
	// Reserve a unique destination using CreateTemp, then publish the
	// fully synced payload through a hard link. Link fails if the name
	// exists, so a concurrent writer cannot overwrite another batch.
	reservation, err := os.CreateTemp(s.dir, fmt.Sprintf("%020d-*.json", time.Now().UnixNano()))
	if err != nil {
		return "", err
	}
	name := reservation.Name()
	if err := reservation.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	if err := os.Link(temp.Name(), name); err != nil {
		return "", err
	}
	if err := syncDir(s.dir); err != nil {
		// The final filename is already visible, although its directory
		// entry may not survive a crash. Retain its path for in-process
		// retries rather than creating another copy of this batch.
		return name, fmt.Errorf("published DNS spool batch %s, directory sync failed: %w", name, err)
	}
	// Remove the staging name before returning: only the committed JSON
	// entry should remain visible after a successful write.
	if err := os.Remove(temp.Name()); err != nil {
		// The committed batch already exists. Return its path so the caller
		// can retry delivery without writing a duplicate spool batch.
		return name, fmt.Errorf("committed DNS spool batch %s, temporary cleanup failed: %w", name, err)
	}
	if err := syncDir(s.dir); err != nil {
		return name, fmt.Errorf("committed DNS spool batch %s, cleanup sync failed: %w", name, err)
	}
	return name, nil
}

func (s *Spool) Load(file string) ([]Event, error) {
	if filepath.Dir(file) != s.dir || !strings.HasSuffix(file, ".json") {
		return nil, errors.New("invalid spool batch path")
	}
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("spool batch is not a regular file")
	}
	payload, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var events []Event
	if err = json.Unmarshal(payload, &events); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, errors.New("empty spool batch")
	}
	return events, nil
}

// Quarantine removes an unreadable batch from the replay queue without deleting
// its bytes. Operators can inspect the .corrupt file before removing it.
func (s *Spool) Quarantine(file string) error {
	if filepath.Dir(file) != s.dir || !strings.HasSuffix(file, ".json") {
		return errors.New("invalid spool quarantine path")
	}
	if err := os.Rename(file, file+".corrupt"); err != nil {
		return err
	}
	return syncDir(s.dir)
}

func (s *Spool) Ack(file string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if filepath.Dir(file) != s.dir {
		return errors.New("spool path outside directory")
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(s.dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
