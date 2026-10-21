package allocation

import (
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/wraplink/dns-control/internal/domain"
)

type Store struct {
	mu sync.RWMutex

	leases map[string]*Lease

	revision uint64

	leaseDuration time.Duration
	roundRobin    map[string]uint64
}

func NewStore(leaseDuration ...time.Duration) *Store {
	duration := 30 * time.Second
	if len(leaseDuration) > 0 && leaseDuration[0] > 0 {
		duration = leaseDuration[0]
	}

	return &Store{
		leases:        make(map[string]*Lease),
		roundRobin:    make(map[string]uint64),
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
		if lease.State != StatePending || strings.Contains(name, "#replica#") || (lease.Type == TypeRoute && lease.Generation > 0 && (lease.AgentID != agentID || net.ParseIP(lease.Address) == nil)) {
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

	// After assigning primary work, distribute existing SNI policies to
	// additional agents. Only one lease per agent/domain is permitted.
	for _, name := range domains {
		if sniAssigned >= sniMax {
			break
		}
		primary := s.leases[name]
		if primary.Type != TypeSNI || primary.State != StateActive {
			continue
		}
		alreadyAssigned := false
		for _, lease := range s.leases {
			if lease.Domain == primary.Domain && lease.AgentID == agentID &&
				(lease.State == StateAssigned || lease.State == StateActive) {
				alreadyAssigned = true
				break
			}
		}
		if alreadyAssigned {
			continue
		}
		key := primary.Domain + "#replica#" + agentID
		s.revision++
		replica := &Lease{
			ID: uuid.NewString(), Domain: primary.Domain, Type: TypeSNI,
			State: StateAssigned, AgentID: agentID, SNI: primary.Domain,
			Revision: s.revision, ExpiresAt: now.Add(s.leaseDuration),
		}
		s.leases[key] = replica
		result = append(result, *replica)
		sniAssigned++
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
			s.removeOrResetExpiredLocked(lease)
			release = append(release, id)
			continue
		}

		lease.ExpiresAt = expiresAt
	}
	return release
}

func (s *Store) Activate(leaseID, agentID, address, sni, routeID string) bool {
	return s.ActivateWithGeneration(leaseID, agentID, address, sni, routeID, 0)
}

func (s *Store) ActivateWithGeneration(leaseID, agentID, address, sni, routeID string, generation int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	lease := s.findByIDLocked(strings.TrimSpace(leaseID))
	if lease == nil || lease.AgentID != strings.TrimSpace(agentID) || lease.State != StateAssigned || (lease.Type == TypeRoute && lease.Generation > 0 && generation != lease.Generation) {
		return false
	}

	now := time.Now().UTC()
	if !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(now) {
		s.removeOrResetExpiredLocked(lease)
		return false
	}

	if lease.Type == TypeRoute && lease.Generation > 0 && lease.Address != strings.TrimSpace(address) {
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

	s.removeOrResetExpiredLocked(lease)
	return true
}

// ActiveCandidates returns a stable ordered snapshot of all non-expired active
// allocations for a domain. It supports multi-agent leases without exposing
// the mutable backing store.
// AddActiveReplica records a separately provisioned agent endpoint for a
// managed domain. It must only be called after the agent reports successful
// provisioning; it does not replace the primary lease.
func (s *Store) AddActiveReplica(domain, agentID, address string) bool {
	domain = normalize(domain)
	agentID = strings.TrimSpace(agentID)
	ip := net.ParseIP(strings.TrimSpace(address))
	if agentID == "" || ip == nil || ip.To4() == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	primary, ok := s.leases[domain]
	if !ok || primary.Type != TypeSNI {
		return false
	}
	for _, lease := range s.leases {
		if lease.Domain == domain && lease.AgentID == agentID && lease.State == StateActive {
			return false
		}
	}
	s.revision++
	key := domain + "#replica#" + agentID
	s.leases[key] = &Lease{
		ID: uuid.NewString(), Domain: domain, Type: TypeSNI,
		State: StateActive, AgentID: agentID, Address: ip.To4().String(),
		Revision: s.revision, ExpiresAt: time.Now().UTC().Add(s.leaseDuration),
	}
	return true
}

func (s *Store) ActiveCandidates(domain string) []Lease {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now().UTC()
	var result []Lease
	for _, lease := range s.leases {
		if lease.Domain != normalize(domain) || lease.State != StateActive {
			continue
		}
		if !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(now) {
			continue
		}
		result = append(result, *lease)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AgentID == result[j].AgentID {
			return result[i].ID < result[j].ID
		}
		return result[i].AgentID < result[j].AgentID
	})
	return result
}

// NextCandidate rotates through the eligible candidates. The predicate can
// exclude offline agents and invalid addresses without consuming a DNS answer.
func (s *Store) NextCandidate(domain string, eligible func(Lease) bool) (*Lease, bool) {
	candidates := s.ActiveCandidates(domain)
	filtered := make([]Lease, 0, len(candidates))
	for _, lease := range candidates {
		if eligible == nil || eligible(lease) {
			filtered = append(filtered, lease)
		}
	}
	if len(filtered) == 0 {
		return nil, false
	}
	s.mu.Lock()
	index := s.roundRobin[normalize(domain)] % uint64(len(filtered))
	s.roundRobin[normalize(domain)]++
	s.mu.Unlock()
	selected := filtered[index]
	return &selected, true
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
	for name, lease := range s.leases {
		if lease.State != StateAssigned && lease.State != StateActive {
			continue
		}
		if lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now) {
			continue
		}
		if strings.Contains(name, "#replica#") {
			delete(s.leases, name)
			s.revision++
		} else {
			s.resetToPendingLocked(lease)
		}
		changed++
	}
	return changed
}

func (s *Store) removeOrResetExpiredLocked(lease *Lease) {
	for key, candidate := range s.leases {
		if candidate == lease && strings.Contains(key, "#replica#") {
			delete(s.leases, key)
			s.revision++
			return
		}
	}
	s.resetToPendingLocked(lease)
}

func (s *Store) resetToPendingLocked(lease *Lease) {
	s.revision++
	lease.State = StatePending
	if lease.Type != TypeRoute || lease.Generation == 0 {
		lease.AgentID = ""
		lease.Address = ""
	}
	lease.SNI = ""
	lease.RouteID = ""
	lease.ExpiresAt = time.Time{}
	lease.Revision = s.revision
}

// ReconcilePolicies removes leases for deleted or changed managed rules and
// creates pending leases for newly introduced SNI/route policies.
// Call only after a domain snapshot has passed validation.
func (s *Store) ReconcilePolicies(rules []domain.Rule) {
	desired := make(map[string]domain.Rule, len(rules))
	for _, rule := range rules {
		if rule.Mode == domain.ModeHijack || (rule.Mode == domain.ModeRoute && rule.Assignment != nil && rule.Assignment.AgentID != "" && rule.Assignment.Generation > 0 && net.ParseIP(rule.Assignment.PublicIP) != nil) {
			desired[normalize(rule.Domain)] = rule
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, lease := range s.leases {
		rule, ok := desired[lease.Domain]
		if !ok || (rule.Mode == domain.ModeHijack && lease.Type != TypeSNI) ||
			(rule.Mode == domain.ModeRoute && (lease.Type != TypeRoute ||
				rule.Route == nil || lease.DestinationIP != rule.Route.DestinationIP ||
				lease.Protocol != rule.Route.Protocol ||
				lease.DestinationPortStart != rule.Route.DestinationPortStart ||
				lease.DestinationPortEnd != rule.Route.DestinationPortEnd ||
				(rule.Assignment != nil && (lease.AgentID != rule.Assignment.AgentID || lease.Address != rule.Assignment.PublicIP || lease.Generation != rule.Assignment.Generation)))) {
			delete(s.leases, name)
			s.revision++
		}
	}
	for name, rule := range desired {
		if _, ok := s.leases[name]; ok {
			continue
		}
		s.revision++
		lease := &Lease{ID: uuid.NewString(), Domain: name, State: StatePending, Revision: s.revision}
		if rule.Mode == domain.ModeHijack {
			lease.Type = TypeSNI
		} else {
			lease.Type = TypeRoute
			lease.DestinationIP = rule.Route.DestinationIP
			lease.Protocol = rule.Route.Protocol
			lease.DestinationPortStart = rule.Route.DestinationPortStart
			lease.DestinationPortEnd = rule.Route.DestinationPortEnd
			if rule.Assignment != nil {
				lease.AgentID = rule.Assignment.AgentID
				lease.Address = rule.Assignment.PublicIP
				lease.Generation = rule.Assignment.Generation
			}
		}
		s.leases[name] = lease
	}
}
