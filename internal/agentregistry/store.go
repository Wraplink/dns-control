package agentregistry

import (
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	StatusOnline  = "online"
	StatusOffline = "offline"
)

type Capacity struct {
	SNI   int `json:"sni"`
	Route int `json:"route"`
}

type Diagnostics struct {
	AdvertisedIPs []string `json:"advertised_ips,omitempty"`
	UpstreamDNS []string `json:"upstream_dns,omitempty"`
	SNIEnabled bool `json:"sni_enabled"`
	SNIListenAddress string `json:"sni_listen_address,omitempty"`
	Version string `json:"version,omitempty"`
}

type Agent struct {
	Diagnostics Diagnostics `json:"diagnostics"`
	ObservedIP string `json:"observed_ip,omitempty"`
	ID           string    `json:"id"`
	Region       string    `json:"region,omitempty"`
	Capacity     Capacity  `json:"capacity"`
	Status       string    `json:"status"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

type Store struct {
	mu     sync.RWMutex
	agents map[string]*Agent
}

func NewStore() *Store {
	return &Store{agents: make(map[string]*Agent)}
}

func (s *Store) Upsert(id, region string, capacity Capacity) Agent {
	id = strings.TrimSpace(id)
	region = strings.TrimSpace(region)
	if capacity.SNI < 0 {
		capacity.SNI = 0
	}
	if capacity.Route < 0 {
		capacity.Route = 0
	}

	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agents[id]
	if !ok {
		agent = &Agent{
			ID:           id,
			RegisteredAt: now,
		}
		s.agents[id] = agent
	}

	agent.Region = region
	agent.Capacity = capacity
	agent.Status = StatusOnline
	agent.LastSeenAt = now

	return *agent
}

func (s *Store) SetDiagnostics(id string, d Diagnostics, observedIP string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if agent := s.agents[id]; agent != nil {
		agent.Diagnostics = d
		agent.ObservedIP = observedIP
	}
}

func (s *Store) Touch(id string) (Agent, bool) {
	id = strings.TrimSpace(id)

	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.agents[id]
	if !ok {
		return Agent{}, false
	}

	agent.Status = StatusOnline
	agent.LastSeenAt = time.Now().UTC()
	return *agent, true
}

func (s *Store) Get(id string) (Agent, bool) {
	id = strings.TrimSpace(id)

	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, ok := s.agents[id]
	if !ok {
		return Agent{}, false
	}
	return *agent, true
}

func (s *Store) MarkOfflineBefore(cutoff time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := 0
	for _, agent := range s.agents {
		if agent.Status == StatusOffline || !agent.LastSeenAt.Before(cutoff) {
			continue
		}
		agent.Status = StatusOffline
		changed++
	}
	return changed
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
