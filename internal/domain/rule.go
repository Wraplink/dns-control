package domain

import (
	"fmt"
	"net"
	"strings"
)

type Rule struct {
	Domain string `yaml:"domain" json:"domain"`
	Mode   Mode   `yaml:"mode" json:"mode"`
	// ConnectionModes scopes managed rules per client. Empty means all modes.
	// Smart always preserves legacy managed DNS policy behavior.
	ConnectionModes []string     `yaml:"connection_modes,omitempty" json:"connection_modes,omitempty"`
	Route           *RoutePolicy `yaml:"route,omitempty" json:"route,omitempty"`
}

type RoutePolicy struct {
	DestinationIP        string `yaml:"destination_ip" json:"destination_ip"`
	Protocol             string `yaml:"protocol" json:"protocol"`
	DestinationPortStart uint16 `yaml:"destination_port_start,omitempty" json:"destination_port_start,omitempty"`
	DestinationPortEnd   uint16 `yaml:"destination_port_end,omitempty" json:"destination_port_end,omitempty"`
}

func (r Rule) Normalize() Rule {
	r.Domain = normalizeDomain(r.Domain)
	for i, mode := range r.ConnectionModes {
		r.ConnectionModes[i] = strings.ToLower(strings.TrimSpace(mode))
	}

	if r.Route != nil {
		route := *r.Route
		route.DestinationIP = strings.TrimSpace(route.DestinationIP)
		route.Protocol = strings.ToLower(strings.TrimSpace(route.Protocol))
		r.Route = &route
	}

	return r
}

func (r Rule) Validate() error {
	if r.Domain == "" {
		return fmt.Errorf("domain cannot be empty")
	}

	if !r.Mode.Valid() {
		return fmt.Errorf(
			"invalid mode %q for domain %q",
			r.Mode,
			r.Domain,
		)
	}

	for _, mode := range r.ConnectionModes {
		if mode != "gaming" && mode != "streaming" {
			return fmt.Errorf("invalid connection_mode %q for domain %q (use gaming or streaming)", mode, r.Domain)
		}
	}

	if r.Mode != ModeRoute {
		if r.Route != nil {
			return fmt.Errorf(
				"route policy is only valid for route mode domain %q",
				r.Domain,
			)
		}
		return nil
	}

	if r.Route == nil {
		return fmt.Errorf(
			"route policy is required for route mode domain %q",
			r.Domain,
		)
	}

	if ip := net.ParseIP(r.Route.DestinationIP); ip == nil || ip.To4() == nil {
		return fmt.Errorf(
			"route destination_ip must be a valid IPv4 address for domain %q",
			r.Domain,
		)
	}

	switch r.Route.Protocol {
	case "tcp", "udp":
	default:
		return fmt.Errorf(
			"route protocol must be tcp or udp for domain %q",
			r.Domain,
		)
	}

	start := r.Route.DestinationPortStart
	end := r.Route.DestinationPortEnd
	if start == 0 && end == 0 {
		return nil
	}
	if start == 0 || end == 0 {
		return fmt.Errorf(
			"route destination port range must specify both start and end for domain %q",
			r.Domain,
		)
	}
	if start > end {
		return fmt.Errorf(
			"route destination port start cannot exceed end for domain %q",
			r.Domain,
		)
	}

	return nil
}

// AppliesTo reports whether a managed rule should affect the requesting user.
// Smart preserves all legacy rules. Unscoped rules apply to all modes.
// Mode-specific rules apply only to their selected gaming/streaming audience.
func (r Rule) AppliesTo(connectionMode string) bool {
	if connectionMode == "" {
		connectionMode = "smart" // backward compatible snapshots
	}
	if connectionMode == "smart" {
		return true
	}
	if connectionMode != "gaming" && connectionMode != "streaming" {
		return false
	}
	if len(r.ConnectionModes) == 0 {
		return true
	}
	for _, mode := range r.ConnectionModes {
		if mode == connectionMode {
			return true
		}
	}
	return false
}

func normalizeDomain(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".")
	value = strings.ToLower(value)

	return value
}
