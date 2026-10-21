package domain

import "testing"

func TestReplaceSnapshotIsAtomic(t *testing.T) {
	store, err := NewStore([]Rule{{Domain: "old.example", Mode: ModeHijack}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Replace([]Rule{
		{Domain: "valid.example", Mode: ModeNormal},
		{Domain: "invalid.example", Mode: ModeSpecificIP, SpecificIP: "invalid"},
	}); err == nil {
		t.Fatal("expected invalid snapshot to fail")
	}
	if _, ok := store.Get("old.example"); !ok {
		t.Fatal("invalid snapshot changed existing rules")
	}
	if _, ok := store.Get("valid.example"); ok {
		t.Fatal("invalid snapshot was partially applied")
	}
	if err := store.Replace([]Rule{{Domain: "new.example", Mode: ModeNormal}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("old.example"); ok {
		t.Fatal("old rule survived replacement")
	}
	if _, ok := store.Get("new.example"); !ok {
		t.Fatal("new rule missing")
	}
}
