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
