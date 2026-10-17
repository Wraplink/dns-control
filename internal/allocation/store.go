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

func NewStore(leaseDuration ...time.Duration) *Store {
	duration := 30 * time.Second
	if len(leaseDuration) > 0 && leaseDuration[0] > 0 {
		duration = leaseDuration[0]
	}

	return &Store{
		leases:        make(map[string]*Lease),
		leaseDuration: duration,
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

func (s *Store) Poll(agentID string, sniMax, routeMax int) []Lease {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || (sniMax <= 0 && routeMax <= 0) {
		return nil
	}
	if sniMax < 0 {
		sniMax = 0
	}
	if routeMax < 0 {
		routeMax = 0
	}

	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.reclaimExpiredLocked(now)

	domains := make([]string, 0, len(s.leases))
	for name := range s.leases {
		domains = append(domains, name)
	}
	sort.Strings(domains)

	result := make([]Lease, 0, sniMax+routeMax)
	sniAssigned := 0
	routeAssigned := 0

	for _, name := range domains {
		lease := s.leases[name]
		if lease.State != StatePending {
			continue
		}

		switch lease.Type {
		case TypeSNI:
			if sniAssigned >= sniMax {
				continue
			}
			sniAssigned++
		case TypeRoute:
			if routeAssigned >= routeMax {
				continue
			}
			routeAssigned++
		default:
			continue
		}

		s.revision++
		lease.State = StateAssigned
		lease.AgentID = agentID
		lease.Revision = s.revision
		lease.ExpiresAt = now.Add(s.leaseDuration)
		result = append(result, *lease)
	}

	return result
}

func (s *Store) Heartbeat(agentID string, leaseIDs []string) []string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || len(leaseIDs) == 0 {
		return nil
	}

	now := time.Now().UTC()
	expiresAt := now.Add(s.leaseDuration)

	s.mu.Lock()
	defer s.mu.Unlock()

	release := make([]string, 0)
	for _, id := range leaseIDs {
		lease := s.findByIDLocked(strings.TrimSpace(id))
		if lease == nil {
			release = append(release, id)
			continue
		}

		if lease.AgentID != agentID ||
			(lease.State != StateAssigned && lease.State != StateActive) {
			release = append(release, id)
			continue
		}

		if !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(now) {
			s.resetToPendingLocked(lease)
			release = append(release, id)
			continue
		}

		lease.ExpiresAt = expiresAt
	}
	return release
}

func (s *Store) Activate(leaseID, agentID, address, sni, routeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	lease := s.findByIDLocked(strings.TrimSpace(leaseID))
	if lease == nil || lease.AgentID != strings.TrimSpace(agentID) || lease.State != StateAssigned {
		return false
	}

	now := time.Now().UTC()
	if !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(now) {
		s.resetToPendingLocked(lease)
		return false
	}

	s.revision++
	lease.State = StateActive
	lease.Address = strings.TrimSpace(address)
	lease.SNI = strings.TrimSpace(sni)
	lease.RouteID = strings.TrimSpace(routeID)
	lease.Revision = s.revision
	lease.ExpiresAt = now.Add(s.leaseDuration)
	return true
}

func (s *Store) Fail(leaseID, agentID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	lease := s.findByIDLocked(strings.TrimSpace(leaseID))
	if lease == nil || lease.AgentID != strings.TrimSpace(agentID) {
		return false
	}
	if lease.State != StateAssigned && lease.State != StateActive {
		return false
	}

	s.resetToPendingLocked(lease)
	return true
}

func (s *Store) Get(domain string) (*Lease, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lease, ok := s.leases[normalize(domain)]
	if !ok || lease.State != StateActive {
		return nil, false
	}
	if !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(time.Now().UTC()) {
		return nil, false
	}

	copy := *lease
	return &copy, true
}

func (s *Store) ReconcileExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reclaimExpiredLocked(time.Now().UTC())
}

func (s *Store) Revision() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision
}

func (s *Store) findByIDLocked(id string) *Lease {
	if id == "" {
		return nil
	}
	for _, lease := range s.leases {
		if lease.ID == id {
			return lease
		}
	}
	return nil
}

func (s *Store) reclaimExpiredLocked(now time.Time) int {
	changed := 0
	for _, lease := range s.leases {
		if lease.State != StateAssigned && lease.State != StateActive {
			continue
		}
		if lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now) {
			continue
		}
		s.resetToPendingLocked(lease)
		changed++
	}
	return changed
}

func (s *Store) resetToPendingLocked(lease *Lease) {
	s.revision++
	lease.State = StatePending
	lease.AgentID = ""
	lease.Address = ""
	lease.SNI = ""
	lease.RouteID = ""
	lease.ExpiresAt = time.Time{}
	lease.Revision = s.revision
}
