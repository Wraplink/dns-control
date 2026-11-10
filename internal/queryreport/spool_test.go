package queryreport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSpoolPersistsAndReplaysEvents(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSpool(dir, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	events := []Event{{EventID: "stable-id", ObservedAt: time.Now().UTC(), Domain: "example.com", QType: "A", RCode: "NOERROR", ResolutionMode: "normal"}}
	path, err := s.Save(events)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewSpool(dir, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	files, err := restarted.Files()
	if err != nil || len(files) != 1 || files[0] != path {
		t.Fatalf("files=%v err=%v", files, err)
	}
	replay, err := restarted.Load(path)
	if err != nil || len(replay) != 1 || replay[0].EventID != "stable-id" {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	if err = restarted.Ack(path); err != nil {
		t.Fatal(err)
	}
	files, err = restarted.Files()
	if err != nil || len(files) != 0 {
		t.Fatalf("files after ack=%v err=%v", files, err)
	}
}

func TestSpoolCapacityAndCorruption(t *testing.T) {
	s, err := NewSpool(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Save([]Event{{EventID: "id", Domain: "example.com"}})
	if !errors.Is(err, ErrSpoolFull) {
		t.Fatalf("want spool full, got %v", err)
	}
	files, err := s.Files()
	if err != nil || len(files) != 0 {
		t.Fatalf("unexpected files %v %v", files, err)
	}
	if err = os.WriteFile(s.dir+"/bad.json", []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Load(s.dir + "/bad.json"); err == nil {
		t.Fatal("corrupt spool file accepted")
	}
}

func TestSpoolAckIsIdempotent(t *testing.T) {
	spool, err := NewSpool(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	path, err := spool.Save([]Event{{EventID: "ack-twice", Domain: "example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := spool.Ack(path); err != nil {
		t.Fatal(err)
	}
	if err := spool.Ack(path); err != nil {
		t.Fatalf("ack after file removal: %v", err)
	}
}

func TestSpoolQuarantinePreservesCorruptBatchAndAllowsReplay(t *testing.T) {
	spool, err := NewSpool(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	bad := spool.dir + "/000-bad.json"
	if err := os.WriteFile(bad, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	good, err := spool.Save([]Event{{EventID: "good", Domain: "example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := spool.Quarantine(bad); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(bad + ".corrupt")
	if err != nil || string(payload) != "{" {
		t.Fatalf("quarantined content=%q err=%v", payload, err)
	}
	files, err := spool.Files()
	if err != nil || len(files) != 1 || files[0] != good {
		t.Fatalf("replay files=%v err=%v", files, err)
	}
}

func TestSpoolCapacityDoesNotEvictExistingBatch(t *testing.T) {
	spool, err := NewSpool(t.TempDir(), 512)
	if err != nil {
		t.Fatal(err)
	}
	first, err := spool.Save([]Event{{EventID: "first", Domain: "example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	spool.maxBytes = info.Size()
	if _, err := spool.Save([]Event{{EventID: "second", Domain: "example.org"}}); !errors.Is(err, ErrSpoolFull) {
		t.Fatalf("expected spool full, got %v", err)
	}
	files, err := spool.Files()
	if err != nil || len(files) != 1 || files[0] != first {
		t.Fatalf("existing batch must survive capacity rejection: files=%v err=%v", files, err)
	}
}

func TestSpoolFilenameDoesNotContainEventID(t *testing.T) {
	spool, err := NewSpool(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	path, err := spool.Save([]Event{{EventID: "../sensitive-event-id", Domain: "example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(filepath.Base(path), "sensitive-event-id") || filepath.Dir(path) != spool.dir {
		t.Fatalf("unsafe spool filename: %s", path)
	}
	events, err := spool.Load(path)
	if err != nil || len(events) != 1 || events[0].EventID != "../sensitive-event-id" {
		t.Fatalf("event data changed: events=%v err=%v", events, err)
	}
}
