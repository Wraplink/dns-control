package publicipsync

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Operation is a requested subscription IP revision. This package defines
// validation only; it does not grant network access or persist state.
type Operation struct {
	SubscriptionID uuid.UUID `json:"subscriptionId"`
	PublicIPID     uuid.UUID `json:"publicIpId"`
	IPAddress      string    `json:"ipAddress"`
	Revision       int64     `json:"revision"`
	AttemptID      uuid.UUID `json:"attemptId"`
}

type NodeReport struct {
	NodeID         string    `json:"nodeId"`
	SubscriptionID uuid.UUID `json:"subscriptionId"`
	PublicIPID     uuid.UUID `json:"publicIpId"`
	Revision       int64     `json:"revision"`
	AttemptID      uuid.UUID `json:"attemptId"`
	Applied        bool      `json:"applied"`
}

var ErrIncomplete = errors.New("public IP network acknowledgement incomplete")
var ErrInvalidOperation = errors.New("invalid public IP operation")

func (o Operation) Validate() error {
	if o.SubscriptionID == uuid.Nil || o.PublicIPID == uuid.Nil || o.AttemptID == uuid.Nil || o.Revision < 1 || strings.TrimSpace(o.IPAddress) == "" {
		return ErrInvalidOperation
	}
	return nil
}

// ValidateReports checks already authenticated reports against a frozen,
// authoritative required-node snapshot. Caller must authenticate each node,
// persist the snapshot, and inspect real applied state before accepting reports.
func ValidateReports(op Operation, required []string, reports []NodeReport) error {
	if err := op.Validate(); err != nil {
		return err
	}
	if len(required) == 0 {
		return ErrIncomplete
	}
	expected := make(map[string]bool, len(required))
	for _, id := range required {
		if strings.TrimSpace(id) == "" || expected[id] {
			return ErrIncomplete
		}
		expected[id] = true
	}
	seen := make(map[string]bool, len(reports))
	for _, report := range reports {
		if !expected[report.NodeID] {
			return fmt.Errorf("%w: unexpected node %q", ErrIncomplete, report.NodeID)
		}
		if seen[report.NodeID] {
			return fmt.Errorf("%w: duplicate node %q", ErrIncomplete, report.NodeID)
		}
		seen[report.NodeID] = true
		if !report.Applied || report.SubscriptionID != op.SubscriptionID || report.PublicIPID != op.PublicIPID || report.Revision != op.Revision || report.AttemptID != op.AttemptID {
			return fmt.Errorf("%w: node %q did not apply expected revision", ErrIncomplete, report.NodeID)
		}
	}
	if len(seen) != len(expected) {
		return ErrIncomplete
	}
	return nil
}
