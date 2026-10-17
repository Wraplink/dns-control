package allocation

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Store struct {
	mu sync.RWMutex

	leases map[string]*Lease

	revision uint64

	leaseDuration time.Duration
}

func NewStore() *Store {
	return &Store{
		leases:        make(map[string]*Lease),
		leaseDuration: 30 * time.Second,
	}
}

func (s *Store) List() []Lease {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Lease, 0, len(s.leases))
	for _, lease := range s.leases {
		result = append(result, *lease)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Domain < result[j].Domain
	})

	return result
}

func normalize(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".")
	return strings.ToLower(value)
}

func (s *Store) EnsurePending(domain string, t Type) {
	domain = normalize(domain)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.leases[domain]; exists {
		return
	}

	s.revision++
	s.leases[domain] = &Lease{
		ID:       uuid.NewString(),
		Domain:   domain,
		Type:     t,
		State:    StatePending,
		Revision: s.revision,
	}
}

// Usage returns assigned + active lease counts owned by an agent.
func (s *Store) Usage(agentID string) (sni int, route int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, lease := range s.leases {
		if lease.AgentID != agentID || (lease.State != StateAssigned && lease.State != StateActive) {
			continue
		}
		switch lease.Type {
		case TypeSNI:
			sni++
		case TypeRoute:
			route++
		}
	}
	return sni, route
}

// Poll assigns pending leases without exceeding the registered per-type capacity.
func (s *Store) Poll(agentID string, maxItems, sniSlots, routeSlots int) []Lease {
	if maxItems <= 0 || (sniSlots <= 0 && routeSlots <= 0) {
		return nil
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	// Timed-out ASSIGNED leases were never activated and are safe to requeue.
	for _, lease := range s.leases {
		if lease.State == StateAssigned && !lease.ExpiresAt.IsZero() && now.After(lease.ExpiresAt) {
			s.requeueLocked(lease)
		}
	}

	result := make([]Lease, 0, maxItems)
	for _, lease := range s.leases {
		if lease.State != StatePending {
			continue
		}

		switch lease.Type {
		case TypeSNI:
			if sniSlots <= 0 {
				continue
			}
			sniSlots--
		case TypeRoute:
			if routeSlots <= 0 {
				continue
			}
			routeSlots--
		default:
			continue
		}

		s.revision++
		lease.State = StateAssigned
		lease.AgentID = agentID
		lease.Revision = s.revision
		lease.ExpiresAt = now.Add(s.leaseDuration)
		result = append(result, *lease)

		if len(result) >= maxItems {
			break
		}
	}

	return result
}

func (s *Store) Heartbeat(agentID string, leaseIDs []string) {
	if agentID == "" || len(leaseIDs) == 0 {
		return
	}

	wanted := make(map[string]struct{}, len(leaseIDs))
	for _, id := range leaseIDs {
		wanted[id] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	expiresAt := time.Now().Add(s.leaseDuration)
	for _, lease := range s.leases {
		if lease.AgentID != agentID {
			continue
		}
		if _, ok := wanted[lease.ID]; ok {
			lease.ExpiresAt = expiresAt
		}
	}
}

func (s *Store) Activate(leaseID, agentID, address, sni, routeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, lease := range s.leases {
		if lease.ID != leaseID {
			continue
		}
		if lease.AgentID != agentID || lease.State != StateAssigned {
			return false
		}

		s.revision++
		lease.State = StateActive
		lease.Address = address
		lease.SNI = sni
		lease.RouteID = routeID
		lease.Revision = s.revision
		lease.ExpiresAt = time.Now().Add(s.leaseDuration)
		return true
	}
	return false
}

// Fail returns a failed assignment to the pending pool immediately.
func (s *Store) Fail(leaseID, agentID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, lease := range s.leases {
		if lease.ID != leaseID {
			continue
		}
		if lease.AgentID != agentID || lease.State != StateAssigned {
			return false
		}
		s.requeueLocked(lease)
		return true
	}
	return false
}

func (s *Store) requeueLocked(lease *Lease) {
	s.revision++
	lease.State = StatePending
	lease.AgentID = ""
	lease.Address = ""
	lease.SNI = ""
	lease.RouteID = ""
	lease.ExpiresAt = time.Time{}
	lease.Revision = s.revision
}

func (s *Store) Get(domain string) (*Lease, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lease, ok := s.leases[normalize(domain)]
	if !ok {
		return nil, false
	}
	copy := *lease
	return &copy, true
}

func (s *Store) Revision() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision
}
