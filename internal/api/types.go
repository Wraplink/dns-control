package api

import (
	"github.com/wraplink/dns-control/internal/agentregistry"
	"github.com/wraplink/dns-control/internal/allocation"
)

type RegisterRequest struct {
	AgentID string `json:"agent_id"`
	Capacity struct {
		SNI   int `json:"sni"`
		Route int `json:"route"`
	} `json:"capacity"`
}

type RegisterResponse struct {
	OK    bool                `json:"ok"`
	Agent agentregistry.Agent `json:"agent"`
}

type PollRequest struct {
	AgentID string `json:"agent_id"`
	Max     int    `json:"max"`
}

type PollResponse struct {
	Revision uint64             `json:"revision"`
	Leases   []allocation.Lease `json:"leases"`
}

type HeartbeatRequest struct {
	AgentID  string   `json:"agent_id"`
	LeaseIDs []string `json:"lease_ids"`
}

type HeartbeatResponse struct { OK bool `json:"ok"` }

type ReportRequest struct {
	AgentID string `json:"agent_id"`
	LeaseID string `json:"lease_id"`
	Success bool   `json:"success"`
	Address string `json:"address,omitempty"`
	SNI     string `json:"sni,omitempty"`
	RouteID string `json:"route_id,omitempty"`
}

type ReportResponse struct { OK bool `json:"ok"` }

type LeasesResponse struct {
	Revision uint64             `json:"revision"`
	Leases   []allocation.Lease `json:"leases"`
}
