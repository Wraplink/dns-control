package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wraplink/dns-control/internal/agentregistry"
	"github.com/wraplink/dns-control/internal/allocation"
	"github.com/wraplink/dns-control/internal/domain"
)

type Handler struct {
	domains     *domain.Store
	allocations *allocation.Store
	agents      *agentregistry.Store
	token       string
}

func NewHandler(
	domains *domain.Store,
	allocations *allocation.Store,
	agents *agentregistry.Store,
	token string,
) http.Handler {
	h := &Handler{
		domains:     domains,
		allocations: allocations,
		agents:      agents,
		token:       token,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/register", h.register)
	mux.HandleFunc("/api/v1/agent/poll", h.poll)
	mux.HandleFunc("/api/v1/agent/heartbeat", h.heartbeat)
	mux.HandleFunc("/api/v1/agent/report", h.report)
	mux.HandleFunc("/api/v1/agents", h.agentsList)
	mux.HandleFunc("/api/v1/domains", h.domainsList)
	mux.HandleFunc("/api/v1/leases", h.handleListLeases)

	return h.auth(mux)
}

func (h *Handler) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.token != "" {
			auth := r.Header.Get("Authorization")
			expected := "Bearer " + h.token
			if auth != expected {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	req.AgentID = strings.TrimSpace(req.AgentID)
	req.Region = strings.TrimSpace(req.Region)
	if req.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "agent_id is required"})
		return
	}
	if req.Capacity.SNI < 0 || req.Capacity.Route < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "capacity cannot be negative"})
		return
	}
	if req.Capacity.SNI == 0 && req.Capacity.Route == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "at least one capacity must be greater than zero"})
		return
	}

	agent := h.agents.Upsert(req.AgentID, req.Region, req.Capacity)
	writeJSON(w, http.StatusOK, RegisterResponse{OK: true, Agent: agent})
}

func (h *Handler) poll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req PollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	req.AgentID = strings.TrimSpace(req.AgentID)
	if req.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "agent_id is required"})
		return
	}
	if req.Available.SNI < 0 || req.Available.Route < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "available capacity cannot be negative"})
		return
	}

	agent, registered := h.agents.Touch(req.AgentID)
	if !registered {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "agent must register before polling"})
		return
	}

	sniAvailable := min(req.Available.SNI, agent.Capacity.SNI)
	routeAvailable := min(req.Available.Route, agent.Capacity.Route)
	leases := h.allocations.Poll(req.AgentID, sniAvailable, routeAvailable)

	writeJSON(w, http.StatusOK, PollResponse{
		Revision: h.allocations.Revision(),
		Leases:   leases,
	})
}

func (h *Handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	req.AgentID = strings.TrimSpace(req.AgentID)
	if req.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "agent_id is required"})
		return
	}

	_, registered := h.agents.Touch(req.AgentID)
	if !registered {
		writeJSON(w, http.StatusOK, HeartbeatResponse{
			OK:              true,
			Registered:      false,
			Revision:        h.allocations.Revision(),
			ReleaseLeaseIDs: req.LeaseIDs,
		})
		return
	}

	releases := h.allocations.Heartbeat(req.AgentID, req.LeaseIDs)
	writeJSON(w, http.StatusOK, HeartbeatResponse{
		OK:              true,
		Registered:      true,
		Revision:        h.allocations.Revision(),
		ReleaseLeaseIDs: releases,
	})
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	req.AgentID = strings.TrimSpace(req.AgentID)
	req.LeaseID = strings.TrimSpace(req.LeaseID)
	if req.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "agent_id is required"})
		return
	}
	if req.LeaseID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "lease_id is required"})
		return
	}
	if _, registered := h.agents.Touch(req.AgentID); !registered {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "agent must register before reporting"})
		return
	}

	if !req.Success {
		if !h.allocations.Fail(req.LeaseID, req.AgentID) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "lease failure report rejected"})
			return
		}
		writeJSON(w, http.StatusOK, ReportResponse{OK: true})
		return
	}

	ok := h.allocations.Activate(req.LeaseID, req.AgentID, req.Address, req.SNI, req.RouteID)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "lease activation rejected"})
		return
	}
	writeJSON(w, http.StatusOK, ReportResponse{OK: true})
}

func (h *Handler) domainsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, h.domains.List())
}

func (h *Handler) agentsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, AgentsResponse{Agents: h.agents.List()})
}

func (h *Handler) handleListLeases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, LeasesResponse{
		Revision: h.allocations.Revision(),
		Leases:   h.allocations.List(),
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
