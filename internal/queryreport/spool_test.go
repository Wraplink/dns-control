package queryreport

import (
	"errors"
	"os"
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
