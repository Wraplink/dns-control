package api

import (
	"encoding/json"
	"context"
	"log"
	"time"

	"github.com/wraplink/dns-control/internal/backendregistry"
	"net"
	"net/http"
	"strings"

	"github.com/wraplink/dns-control/internal/agentregistry"
	"github.com/wraplink/dns-control/internal/allocation"
	"github.com/wraplink/dns-control/internal/dns"
	"github.com/wraplink/dns-control/internal/domain"
	"github.com/wraplink/dns-control/internal/entitlement"
)

type Handler struct {
	domains      *domain.Store
	allocations  *allocation.Store
	agents       *agentregistry.Store
	entitlements *entitlement.Store
	token        string
	resolver     *dns.Resolver
	syncStatus   *DomainSyncStatus
	backendRoutes *backendregistry.Client
}

func NewHandler(
	domains *domain.Store,
	allocations *allocation.Store,
	agents *agentregistry.Store,
	entitlements *entitlement.Store,
	token string,
	resolvers ...*dns.Resolver,
) http.Handler {
	return newHandler(domains, allocations, agents, entitlements, token, nil, resolvers...)
}

func NewHandlerWithSyncStatus(domains *domain.Store, allocations *allocation.Store, agents *agentregistry.Store, entitlements *entitlement.Store, token string, status *DomainSyncStatus, resolvers ...*dns.Resolver) http.Handler {
	return newHandler(domains, allocations, agents, entitlements, token, status, resolvers...)
}

func newHandler(domains *domain.Store, allocations *allocation.Store, agents *agentregistry.Store, entitlements *entitlement.Store, token string, status *DomainSyncStatus, resolvers ...*dns.Resolver) http.Handler {
	h := &Handler{
		domains:      domains,
		allocations:  allocations,
		agents:       agents,
		entitlements: entitlements,
		token:        token,
		syncStatus:   status,
	}

	if len(resolvers) > 0 {
		h.resolver = resolvers[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/dns/resolve", h.resolveDNS)
	mux.HandleFunc("/api/v1/agent/register", h.register)
	mux.HandleFunc("/api/v1/agent/poll", h.poll)
	mux.HandleFunc("/api/v1/agent/heartbeat", h.heartbeat)
	mux.HandleFunc("/api/v1/agent/report", h.report)
	mux.HandleFunc("/api/v1/agents", h.agentsList)
	mux.HandleFunc("/api/v1/access/allowed-ips", h.allowedIPs)
	mux.HandleFunc("/api/v1/domains", h.domainsList)
	mux.HandleFunc("/api/v1/domains/sync-status", h.domainSyncStatus)
	mux.HandleFunc("/api/v1/leases", h.handleListLeases)

	return h.auth(mux)
}

// SetRouteReporter configures durable backend state synchronization. The periodic
// reconciler retries reports after temporary backend failures.
func (h *Handler) SetRouteReporter(client *backendregistry.Client) {
	h.backendRoutes = client
}

func (h *Handler) SyncActiveRoutes(ctx context.Context) {
	if h.backendRoutes == nil {
		return
	}
	for _, lease := range h.allocations.List() {
		if lease.Type != allocation.TypeRoute || lease.State != allocation.StateActive || lease.Generation <= 0 {
			continue
		}
		if err := h.backendRoutes.ReportRouteActivation(ctx, backendregistry.RouteActivation{
			Domain: lease.Domain, AgentID: lease.AgentID, PublicIP: lease.Address,
			Generation: lease.Generation, Active: true,
		}); err != nil {
			log.Printf("backend route activation sync failed domain=%s generation=%d: %v", lease.Domain, lease.Generation, err)
		}
	}
}

func (h *Handler) RunRouteReporter(ctx context.Context, interval time.Duration) {
	if interval <= 0 { interval = 10 * time.Second }
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		h.SyncActiveRoutes(ctx)
		select {
		case <-ctx.Done(): return
		case <-ticker.C:
		}
	}
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
	observedIP, _, splitErr := net.SplitHostPort(r.RemoteAddr)
	if splitErr != nil {
		observedIP = r.RemoteAddr
	}
	h.agents.SetDiagnostics(req.AgentID, req.Diagnostics, observedIP)
	agent, _ = h.agents.Get(req.AgentID)
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

	entitlementRevision, grants := h.entitlementDelta(req.EntitlementRevision)
	entitlementEnforced := h.entitlements != nil

	_, registered := h.agents.Touch(req.AgentID)
	if !registered {
		writeJSON(w, http.StatusOK, HeartbeatResponse{
			OK:                  true,
			Registered:          false,
			Revision:            h.allocations.Revision(),
			ReleaseLeaseIDs:     req.LeaseIDs,
			EntitlementEnforced: entitlementEnforced,
			EntitlementRevision: entitlementRevision,
			Entitlements:        grants,
		})
		return
	}

	releases := h.allocations.Heartbeat(req.AgentID, req.LeaseIDs)
	writeJSON(w, http.StatusOK, HeartbeatResponse{
		OK:                  true,
		Registered:          true,
		Revision:            h.allocations.Revision(),
		ReleaseLeaseIDs:     releases,
		EntitlementEnforced: entitlementEnforced,
		EntitlementRevision: entitlementRevision,
		Entitlements:        grants,
	})
}

func (h *Handler) entitlementDelta(agentRevision uint64) (uint64, []entitlement.Grant) {
	if h.entitlements == nil {
		return 0, nil
	}

	revision, grants := h.entitlements.AccessSnapshot()
	if revision == agentRevision {
		return revision, nil
	}
	return revision, grants
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
		if !h.allocations.FailWithGeneration(req.LeaseID, req.AgentID, req.Generation) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "lease failure report rejected"})
			return
		}
		writeJSON(w, http.StatusOK, ReportResponse{OK: true})
		return
	}

	ok := h.allocations.ActivateWithGeneration(req.LeaseID, req.AgentID, req.Address, req.SNI, req.RouteID, req.Generation)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "lease activation rejected"})
		return
	}
	// The local lease is already active. A background reconciliation retries
	// backend acknowledgments if this request fails or the backend is offline.
	if h.backendRoutes != nil && req.Generation > 0 {
		if lease, found := h.allocations.GetByID(req.LeaseID); found && lease.Type == allocation.TypeRoute {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			if err := h.backendRoutes.ReportRouteActivation(ctx, backendregistry.RouteActivation{
				Domain: lease.Domain, AgentID: req.AgentID, PublicIP: req.Address,
				Generation: req.Generation, Active: true,
			}); err != nil {
				log.Printf("backend route activation report pending retry lease=%s: %v", req.LeaseID, err)
			}
		}
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

// SetDomainSyncStatus connects the live sync tracker to the API handler.
func (h *Handler) domainSyncStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.syncStatus == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "sync status unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, h.syncStatus.Snapshot())
}

// allowedIPs is protected by the existing control API bearer authentication.
func (h *Handler) allowedIPs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.token == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "diagnostics require an API token"})
		return
	}
	if h.entitlements == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enforced": false, "fresh": false, "revision": 0, "allowed_ips": []any{}})
		return
	}
	revision, fresh, grants := h.entitlements.InspectAccess()
	writeJSON(w, http.StatusOK, map[string]any{
		"enforced":    true,
		"fresh":       fresh,
		"revision":    revision,
		"count":       len(grants),
		"allowed_ips": grants,
	})
}
