package queryreport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrSpoolFull = errors.New("dns query spool capacity exceeded")

// Spool persists batches before transmission. Acknowledged batches are removed.
// A replay can contain already accepted events; Backend deduplicates by event ID.
type Spool struct {
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

func (s *Spool) Files() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, filepath.Join(s.dir, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func (s *Spool) Save(events []Event) (string, error) {
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
		info, err := os.Stat(file)
		if err != nil {
			return "", err
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
	name := filepath.Join(s.dir, fmt.Sprintf("%020d-%d.json", time.Now().UnixNano(), os.Getpid()))
	if err = os.Rename(temp.Name(), name); err != nil {
		return "", err
	}
	if err = syncDir(s.dir); err != nil {
		return "", err
	}
	return name, nil
}

func (s *Spool) Load(file string) ([]Event, error) {
	if filepath.Dir(file) != s.dir {
		return nil, errors.New("spool path outside directory")
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
