package entitlement

import (
	"net"
	"sort"
	"sync"
	"time"
)

type Item struct {
	EntitlementID  string    `json:"entitlementId"`
	UserID         string    `json:"userId"`
	SubscriptionID string    `json:"subscriptionId"`
	ClientIP       string    `json:"clientIp"`
	PlanCode       string    `json:"planCode"`
	ConnectionMode string    `json:"connectionMode"`
	DataLimitBytes int64     `json:"dataLimitBytes"`
	UsedBytes      int64     `json:"usedBytes"`
	RemainingBytes int64     `json:"remainingBytes"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

type Snapshot struct {
	GeneratedAt  time.Time `json:"generatedAt"`
	Entitlements []Item    `json:"entitlements"`
}

type Grant struct {
	EntitlementID  string    `json:"entitlementId"`
	SubscriptionID string    `json:"subscriptionId"`
	ClientIP       string    `json:"clientIp"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

type Store struct {
	mu sync.RWMutex

	byIP           map[string]Item
	refreshedAt    time.Time
	maxStale       time.Duration
	accessRevision uint64
	accessStale    bool
}

func NewStore(maxStale time.Duration) *Store {
	return &Store{
		byIP:     make(map[string]Item),
		maxStale: maxStale,
	}
}

func (s *Store) Replace(snapshot Snapshot) {
	now := time.Now().UTC()
	items := make(map[string]Item, len(snapshot.Entitlements))

	for _, item := range snapshot.Entitlements {
		ip := canonicalIPv4(item.ClientIP)
		if ip == "" || !item.ExpiresAt.After(now) || item.RemainingBytes <= 0 {
			continue
		}
		item.ClientIP = ip
		if item.ConnectionMode == "" {
			item.ConnectionMode = "smart" // legacy backend snapshots
		}
		if item.ConnectionMode != "smart" && item.ConnectionMode != "gaming" && item.ConnectionMode != "streaming" {
			continue // reject invalid policy modes rather than apply the wrong rule
		}
		items[ip] = item
	}

	s.mu.Lock()
	if s.accessStale || !sameAccessSet(s.byIP, items) {
		s.accessRevision++
	}
	s.byIP = items
	s.refreshedAt = now
	s.accessStale = false
	s.mu.Unlock()
}

func (s *Store) Allowed(ip net.IP) bool {
	if ip == nil {
		return false
	}

	key := canonicalIPv4(ip.String())
	if key == "" {
		return false
	}

	now := time.Now().UTC()

	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.freshLocked(now) {
		return false
	}

	item, ok := s.byIP[key]
	if !ok {
		return false
	}

	return item.ExpiresAt.After(now) && item.RemainingBytes > 0
}

func (s *Store) Get(ip net.IP) (Item, bool) {
	if !s.Allowed(ip) {
		return Item{}, false
	}

	key := canonicalIPv4(ip.String())
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.byIP[key]
	return item, ok
}

func (s *Store) AccessSnapshot() (uint64, []Grant) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.freshLocked(now) {
		if !s.accessStale {
			s.accessRevision++
			s.accessStale = true
		}
		return s.accessRevision, []Grant{}
	}

	result := make([]Grant, 0, len(s.byIP))
	for _, item := range s.byIP {
		if !item.ExpiresAt.After(now) || item.RemainingBytes <= 0 {
			continue
		}
		result = append(result, Grant{
			EntitlementID:  item.EntitlementID,
			SubscriptionID: item.SubscriptionID,
			ClientIP:       item.ClientIP,
			ExpiresAt:      item.ExpiresAt,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ClientIP < result[j].ClientIP
	})

	return s.accessRevision, result
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byIP)
}

func (s *Store) RefreshedAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.refreshedAt
}

func (s *Store) freshLocked(now time.Time) bool {
	if s.refreshedAt.IsZero() {
		return false
	}
	return s.maxStale <= 0 || now.Sub(s.refreshedAt) <= s.maxStale
}

func sameAccessSet(a, b map[string]Item) bool {
	if len(a) != len(b) {
		return false
	}

	for ip, left := range a {
		right, ok := b[ip]
		if !ok {
			return false
		}
		if left.EntitlementID != right.EntitlementID ||
			left.SubscriptionID != right.SubscriptionID ||
			!left.ExpiresAt.Equal(right.ExpiresAt) {
			return false
		}
	}

	return true
}

func canonicalIPv4(value string) string {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil {
		return ""
	}
	return ip.To4().String()
}

// InspectAccess returns a snapshot of effective, currently authorized client IPs.
func (s *Store) InspectAccess() (uint64, bool, []Grant) {
	revision, grants := s.AccessSnapshot()
	s.mu.RLock()
	fresh := s.freshLocked(time.Now().UTC())
	s.mu.RUnlock()
	return revision, fresh, grants
}
