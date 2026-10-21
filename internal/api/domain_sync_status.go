package api

import (
	"sync"
	"time"
)

// DomainSyncStatus describes the most recent backend policy synchronization.
// A successful fetch is not proof of DNS resolution or edge-agent provisioning.
type DomainSyncStatus struct {
	mu sync.RWMutex
	Enabled bool `json:"enabled"`
	BackendRevision uint64 `json:"backend_revision"`
	SyncedDomains int `json:"synced_domains"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastAttempt *time.Time `json:"last_attempt,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

func (s *DomainSyncStatus) Record(revision uint64, count int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.LastAttempt = &now
	if err != nil {
		s.LastError = err.Error()
		return
	}
	s.BackendRevision = revision
	s.SyncedDomains = count
	s.LastSuccess = &now
	s.LastError = ""
}

func (s *DomainSyncStatus) Snapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{
		"enabled":s.Enabled,
		"backend_revision":s.BackendRevision,
		"synced_domains":s.SyncedDomains,
		"last_success":s.LastSuccess,
		"last_attempt":s.LastAttempt,
		"last_error":s.LastError,
		"healthy":s.Enabled && s.LastSuccess != nil && s.LastError == "",
	}
}
