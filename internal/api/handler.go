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

func NewHandler(domains *domain.Store, allocations *allocation.Store, agents *agentregistry.Store, token string) http.Handler {
	h := &Handler{domains: domains, allocations: allocations, agents: agents, token: token}
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
		if h.token != "" && r.Header.Get("Authorization") != "Bearer "+h.token {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
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
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	req.AgentID = strings.TrimSpace(req.AgentID)
	if req.AgentID == "" || req.Capacity.SNI < 0 || req.Capacity.Route < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid agent registration"})
		return
	}
	agent := h.agents.Register(req.AgentID, req.Capacity.SNI, req.Capacity.Route)
	writeJSON(w, http.StatusOK, RegisterResponse{OK: true, Agent: agent})
}

func (h *Handler) poll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req PollRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	req.AgentID = strings.TrimSpace(req.AgentID)
	agent, ok := h.agents.Get(req.AgentID)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "agent must register before polling"})
		return
	}
	if req.Max <= 0 {
		req.Max = 1
	}
	usedSNI, usedRoute := h.allocations.Usage(req.AgentID)
	sniSlots := agent.SNICapacity - usedSNI
	routeSlots := agent.RouteCapacity - usedRoute
	leases := h.allocations.Poll(req.AgentID, req.Max, sniSlots, routeSlots)
	writeJSON(w, http.StatusOK, PollResponse{Revision: h.allocations.Revision(), Leases: leases})
}

func (h *Handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req HeartbeatRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	req.AgentID = strings.TrimSpace(req.AgentID)
	if !h.agents.Heartbeat(req.AgentID) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "agent must register before heartbeat"})
		return
	}
	h.allocations.Heartbeat(req.AgentID, req.LeaseIDs)
	writeJSON(w, http.StatusOK, HeartbeatResponse{OK: true})
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ReportRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.LeaseID = strings.TrimSpace(req.LeaseID)
	if req.AgentID == "" || req.LeaseID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "agent_id and lease_id are required"})
		return
	}
	if !req.Success {
		if !h.allocations.Fail(req.LeaseID, req.AgentID) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "lease failure rejected"})
			return
		}
		writeJSON(w, http.StatusOK, ReportResponse{OK: true})
		return
	}
	if !h.allocations.Activate(req.LeaseID, req.AgentID, req.Address, req.SNI, req.RouteID) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "lease activation rejected"})
		return
	}
	writeJSON(w, http.StatusOK, ReportResponse{OK: true})
}

func (h *Handler) agentsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, h.agents.List())
}

func (h *Handler) domainsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, h.domains.List())
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *Handler) handleListLeases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, LeasesResponse{Revision: h.allocations.Revision(), Leases: h.allocations.List()})
}
