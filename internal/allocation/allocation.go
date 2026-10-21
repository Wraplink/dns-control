package allocation

import "time"

type State string

const (
	StatePending  State = "pending"
	StateAssigned State = "assigned"
	StateActive   State = "active"
	StateFailed   State = "failed"
)

type Type string

const (
	TypeSNI   Type = "sni"
	TypeRoute Type = "route"
)

type Lease struct {
	ID string `json:"id"`

	// DNS policy
	Domain string `json:"domain"`
	Type   Type   `json:"type"`
	State  State  `json:"state"`

	// Edge ownership
	AgentID string `json:"agent_id,omitempty"`
	Address string `json:"address,omitempty"`

	// SNI dataplane
	SNI string `json:"sni,omitempty"`

	// Route dataplane
	RouteID string `json:"route_id,omitempty"`

	Generation int64 `json:"generation,omitempty"`

	DestinationIP string `json:"destination_ip,omitempty"`
	Protocol      string `json:"protocol,omitempty"`

	// 0 means unspecified.
	// For route leases, zero/zero means all ports (1-65535).
	DestinationPortStart uint16 `json:"destination_port_start,omitempty"`
	DestinationPortEnd   uint16 `json:"destination_port_end,omitempty"`

	// Monotonically increasing control-plane revision.
	Revision uint64 `json:"revision"`

	// Assignment/heartbeat expiration.
	ExpiresAt time.Time `json:"expires_at"`
}
