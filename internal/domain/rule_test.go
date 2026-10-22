package domain

import "testing"

func TestRouteRuleNormalizesAndValidates(t *testing.T) {
	rule := Rule{
		Domain: " Game.Example.COM. ",
		Mode:   ModeRoute,
		Route: &RoutePolicy{
			DestinationIP:        " 192.0.2.20 ",
			Protocol:             " UDP ",
			DestinationPortStart: 30000,
			DestinationPortEnd:   31000,
		},
	}.Normalize()

	if rule.Domain != "game.example.com" {
		t.Fatalf("unexpected normalized domain %q", rule.Domain)
	}
	if rule.Route.DestinationIP != "192.0.2.20" {
		t.Fatalf("unexpected destination IP %q", rule.Route.DestinationIP)
	}
	if rule.Route.Protocol != "udp" {
		t.Fatalf("unexpected protocol %q", rule.Route.Protocol)
	}
	if err := rule.Validate(); err != nil {
		t.Fatalf("expected valid route rule: %v", err)
	}
}

func TestRouteRuleRequiresPolicy(t *testing.T) {
	rule := Rule{
		Domain: "game.example.com",
		Mode:   ModeRoute,
	}.Normalize()

	if err := rule.Validate(); err == nil {
		t.Fatal("expected route mode without route policy to fail")
	}
}

func TestRouteRuleRejectsInvalidIPv4(t *testing.T) {
	rule := Rule{
		Domain: "game.example.com",
		Mode:   ModeRoute,
		Route: &RoutePolicy{
			DestinationIP: "not-an-ip",
			Protocol:      "udp",
		},
	}.Normalize()

	if err := rule.Validate(); err == nil {
		t.Fatal("expected invalid destination IP to fail")
	}
}

func TestRouteRuleRejectsInvalidProtocol(t *testing.T) {
	rule := Rule{
		Domain: "game.example.com",
		Mode:   ModeRoute,
		Route: &RoutePolicy{
			DestinationIP: "192.0.2.20",
			Protocol:      "icmp",
		},
	}.Normalize()

	if err := rule.Validate(); err == nil {
		t.Fatal("expected unsupported protocol to fail")
	}
}

func TestRouteRuleRejectsPartialPortRange(t *testing.T) {
	rule := Rule{
		Domain: "game.example.com",
		Mode:   ModeRoute,
		Route: &RoutePolicy{
			DestinationIP:        "192.0.2.20",
			Protocol:             "tcp",
			DestinationPortStart: 443,
		},
	}.Normalize()

	if err := rule.Validate(); err == nil {
		t.Fatal("expected partial port range to fail")
	}
}

func TestNonRouteRuleRejectsRoutePolicy(t *testing.T) {
	rule := Rule{
		Domain: "game.example.com",
		Mode:   ModeNormal,
		Route: &RoutePolicy{
			DestinationIP: "192.0.2.20",
			Protocol:      "tcp",
		},
	}.Normalize()

	if err := rule.Validate(); err == nil {
		t.Fatal("expected route policy on normal mode to fail")
	}
}

func TestConnectionModeMatching(t *testing.T) {
	gaming := Rule{
		Domain:          "playstation.com",
		Mode:            ModeHijack,
		ConnectionModes: []string{" GAMING "},
	}.Normalize()
	if err := gaming.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{"smart", true},
		{"gaming", true},
		{"streaming", false},
		{"", true}, // legacy entitlement treated as smart
		{"invalid", false},
	} {
		if got := gaming.AppliesTo(tc.mode); got != tc.want {
			t.Errorf("mode=%q: got %t, want %t", tc.mode, got, tc.want)
		}
	}
	if got := (Rule{Mode: ModeHijack}).AppliesTo("streaming"); !got {
		t.Fatal("legacy untagged rule must work in all known modes")
	}
}

func TestConnectionModeRejectsUnsupportedSelector(t *testing.T) {
	rule := Rule{
		Domain:          "example.com",
		Mode:            ModeHijack,
		ConnectionModes: []string{"speed-booster"},
	}
	if err := rule.Validate(); err == nil {
		t.Fatal("expected unknown mode selector to be rejected")
	}
}

func TestSpecificIPRule(t *testing.T) {
	rule := Rule{Domain: " Example.COM. ", Mode: ModeSpecificIP, SpecificIP: " 192.0.2.30 "}.Normalize()
	if rule.Domain != "example.com" || rule.SpecificIP != "192.0.2.30" {
		t.Fatalf("unexpected normalization: %+v", rule)
	}
	if err := rule.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Rule{
		{Domain: "example.com", Mode: ModeSpecificIP},
		{Domain: "example.com", Mode: ModeSpecificIP, SpecificIP: "invalid"},
		{Domain: "example.com", Mode: ModeNormal, SpecificIP: "192.0.2.30"},
		{Domain: "example.com", Mode: ModeSpecificIP, SpecificIP: "192.0.2.30", Route: &RoutePolicy{}},
	} {
		if err := invalid.Normalize().Validate(); err == nil {
			t.Fatalf("expected invalid policy to fail: %+v", invalid)
		}
	}
}

func TestRouteRuleAcceptsCombinedTCPUDP(t *testing.T) {
	rule := Rule{
		Domain: " Game.Example. ",
		Mode:   ModeRoute,
		Route: &RoutePolicy{
			DestinationIP: "192.0.2.20",
			Protocol:      " TCP_UDP ",
		},
	}.Normalize()
	if rule.Route.Protocol != "tcp_udp" {
		t.Fatalf("unexpected protocol %q", rule.Route.Protocol)
	}
	if err := rule.Validate(); err != nil {
		t.Fatalf("combined protocol should be accepted: %v", err)
	}
}

func TestUnresolvedRouteRequiresNoAssignment(t *testing.T) {
	pending := Rule{
		Domain: "pending.example",
		Mode:   ModeRoute,
		Route:  &RoutePolicy{Protocol: "tcp_udp"},
	}
	if err := pending.Validate(); err != nil {
		t.Fatalf("unassigned route awaiting resolution should sync: %v", err)
	}
	pending.Assignment = &RouteAssignment{AgentID: "edge-a", PublicIP: "192.0.2.10", Generation: 1}
	if err := pending.Validate(); err == nil {
		t.Fatal("assigned route without resolved destination must be rejected")
	}
}
