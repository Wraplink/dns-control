package allocation

import (
	"strings"

	"github.com/google/uuid"
)

func (s *Store) EnsureRoutePending(
	domain string,
	destinationIP string,
	protocol string,
	destinationPortStart uint16,
	destinationPortEnd uint16,
) {
	domain = normalize(domain)
	destinationIP = strings.TrimSpace(destinationIP)
	protocol = strings.ToLower(strings.TrimSpace(protocol))

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.leases[domain]; exists {
		return
	}

	s.revision++
	s.leases[domain] = &Lease{
		ID:                   uuid.NewString(),
		Domain:               domain,
		Type:                 TypeRoute,
		State:                StatePending,
		DestinationIP:        destinationIP,
		Protocol:             protocol,
		DestinationPortStart: destinationPortStart,
		DestinationPortEnd:   destinationPortEnd,
		Revision:             s.revision,
	}
}
