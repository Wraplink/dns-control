package domain

import (
	"fmt"
	"sync"
)

type Store struct {
	mu    sync.RWMutex
	rules map[string]Rule
}

func NewStore(rules []Rule) (*Store, error) {
	s := &Store{
		rules: make(map[string]Rule),
	}

	for _, rule := range rules {
		rule = rule.Normalize()

		if err := rule.Validate(); err != nil {
			return nil, err
		}

		if _, exists := s.rules[rule.Domain]; exists {
			return nil, fmt.Errorf(
				"duplicate domain rule %q",
				rule.Domain,
			)
		}

		s.rules[rule.Domain] = rule
	}

	return s, nil
}

// Replace atomically publishes a validated domain-policy snapshot.
// Invalid or duplicate policies leave the current snapshot unchanged.
func (s *Store) Replace(rules []Rule) error {
	next, err := NewStore(rules)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.rules = next.rules
	s.mu.Unlock()
	return nil
}

func (s *Store) Get(domain string) (Rule, bool) {
	domain = normalizeDomain(domain)

	s.mu.RLock()
	defer s.mu.RUnlock()

	rule, ok := s.rules[domain]

	return rule, ok
}

func (s *Store) Match(domain string) (Rule, bool) {
	domain = normalizeDomain(domain)

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Exact match first.
	if rule, ok := s.rules[domain]; ok {
		return rule, true
	}

	// Then parent-domain matching.
	//
	// Example:
	//
	// foo.game.example.com
	//
	// can match:
	//
	// game.example.com
	//
	// if that rule exists.
	for {
		index := -1

		for i := 0; i < len(domain); i++ {
			if domain[i] == '.' {
				index = i
				break
			}
		}

		if index < 0 {
			break
		}

		domain = domain[index+1:]

		if rule, ok := s.rules[domain]; ok {
			return rule, true
		}
	}

	return Rule{}, false
}

func (s *Store) List() []Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Rule, 0, len(s.rules))

	for _, rule := range s.rules {
		result = append(result, rule)
	}

	return result
}
