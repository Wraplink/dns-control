package entitlement

import (
	"net"
	"sync"
	"time"
)

type Item struct {
	EntitlementID  string    `json:"entitlementId"`
	UserID         string    `json:"userId"`
	SubscriptionID string    `json:"subscriptionId"`
	ClientIP       string    `json:"clientIp"`
	PlanCode       string    `json:"planCode"`
	DataLimitBytes int64     `json:"dataLimitBytes"`
	UsedBytes      int64     `json:"usedBytes"`
	RemainingBytes int64     `json:"remainingBytes"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

type Snapshot struct {
	GeneratedAt  time.Time `json:"generatedAt"`
	Entitlements []Item    `json:"entitlements"`
}

type Store struct {
	mu sync.RWMutex

	byIP       map[string]Item
	refreshedAt time.Time
	maxStale   time.Duration
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
		items[ip] = item
	}

	s.mu.Lock()
	s.byIP = items
	s.refreshedAt = now
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

	if s.refreshedAt.IsZero() {
		return false
	}
	if s.maxStale > 0 && now.Sub(s.refreshedAt) > s.maxStale {
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

func canonicalIPv4(value string) string {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil {
		return ""
	}
	return ip.To4().String()
}
