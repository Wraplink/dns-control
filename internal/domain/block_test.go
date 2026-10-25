package domain

import "testing"

func TestBlockRule(t *testing.T) {
	rule := Rule{Domain: "blocked.example", Mode: ModeBlock}.Normalize()
	if err := rule.Validate(); err != nil {
		t.Fatalf("block rule rejected: %v", err)
	}
	if rule.Mode != ModeBlock {
		t.Fatalf("unexpected mode: %q", rule.Mode)
	}
	if err := (Rule{Domain: "blocked.example", Mode: ModeBlock, SpecificIP: "192.0.2.1"}).Validate(); err == nil {
		t.Fatal("block rule must reject specific IP")
	}
}
