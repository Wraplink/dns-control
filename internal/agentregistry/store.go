package agentregistry

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusOnline  Status = "online"
	StatusOffline Status = "offline"
)

type Agent struct {
	ID            string    `json:"id"`
	SNICapacity   int       `json:"sni_capacity"`
	RouteCapacity int       `json:"route_capacity"`
	Status        Status    `json:"status"`
	RegisteredAt  time.Time `json:"registered_at"`
	LastSeenAt    time.Time `json:"last_seen_at"`
}

type Store struct {
	mu     sync.RWMutex
	agents map[string]*Agent
}

func NewStore() *Store {
	return &Store{agents: make(map[string]*Agent)}
}

func normalizeID(value string) string {
	return strings.TrimSpace(value)
}

func (s *Store) Register(id string, sniCapacity, routeCapacity int) Agent {
	id = normalizeID(id)
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	agent, exists := s.agents[id]
	if !exists {
		agent = &Agent{
			ID:           id,
			RegisteredAt: now,
		}
		s.agents[id] = agent
	}

	agent.SNICapacity = max(sniCapacity, 0)
	agent.RouteCapacity = max(routeCapacity, 0)
	agent.Status = StatusOnline
	agent.LastSeenAt = now

	return *agent
}

func (s *Store) Heartbeat(id string) bool {
	id = normalizeID(id)

	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agents[id]
	if !ok {
		return false
	}

	agent.Status = StatusOnline
	agent.LastSeenAt = time.Now().UTC()
	return true
}

func (s *Store) Get(id string) (Agent, bool) {
	id = normalizeID(id)

	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, ok := s.agents[id]
	if !ok {
		return Agent{}, false
	}

	return *agent, true
}

func (s *Store) List() []Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Agent, 0, len(s.agents))
	for _, agent := range s.agents {
		result = append(result, *agent)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	return result
}

func (s *Store) MarkOfflineBefore(cutoff time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, agent := range s.agents {
		if agent.Status == StatusOnline && agent.LastSeenAt.Before(cutoff) {
			agent.Status = StatusOffline
			count++
		}
	}
	return count
}
