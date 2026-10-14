package api

import (
	"github.com/wraplink/dns-control/internal/agentregistry"
	"github.com/wraplink/dns-control/internal/allocation"
)

type RegisterRequest struct {
	AgentID  string                 `json:"agent_id"`
	Region   string                 `json:"region,omitempty"`
	Capacity agentregistry.Capacity `json:"capacity"`
}

type RegisterResponse struct {
	OK    bool                `json:"ok"`
	Agent agentregistry.Agent `json:"agent"`
}

type PollRequest struct {
	AgentID   string                 `json:"agent_id"`
	Available agentregistry.Capacity `json:"available"`
}

type PollResponse struct {
	Revision uint64             `json:"revision"`
	Leases   []allocation.Lease `json:"leases"`
}

type HeartbeatRequest struct {
	AgentID  string   `json:"agent_id"`
	LeaseIDs []string `json:"lease_ids"`
}

type HeartbeatResponse struct {
	OK              bool     `json:"ok"`
	Registered      bool     `json:"registered"`
	Revision        uint64   `json:"revision"`
	ReleaseLeaseIDs []string `json:"release_lease_ids,omitempty"`
}

type ReportRequest struct {
	AgentID string `json:"agent_id"`
	LeaseID string `json:"lease_id"`

	Success bool `json:"success"`

	Address string `json:"address,omitempty"`
	SNI     string `json:"sni,omitempty"`
	RouteID string `json:"route_id,omitempty"`
}

type ReportResponse struct {
	OK bool `json:"ok"`
}

type LeasesResponse struct {
	Revision uint64             `json:"revision"`
	Leases   []allocation.Lease `json:"leases"`
}

type AgentsResponse struct {
	Agents []agentregistry.Agent `json:"agents"`
}
