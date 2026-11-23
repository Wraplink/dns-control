package publicipsync

import (
	"context"
	"errors"
)

var ErrUnauthenticatedNode = errors.New("public IP acknowledgement requires authenticated node identity")

// AuthenticatedNode is constructed only by the control-plane authentication
// boundary. Do not derive it from a request JSON body or an untrusted header.
type AuthenticatedNode struct {
	ID string
}

// ReportFromAuthenticatedNode ignores any node identity supplied by the report
// and requires a trusted identity supplied by the caller. The verifier still
// must independently inspect the installed dataplane for the exact operation.
func (t *Tracker) ReportFromAuthenticatedNode(ctx context.Context, node AuthenticatedNode, report NodeReport, verifier DataplaneVerifier) error {
	if node.ID == "" {
		return ErrUnauthenticatedNode
	}
	if report.NodeID != "" && report.NodeID != node.ID {
		return ErrUnauthenticatedNode
	}
	report.NodeID = node.ID
	return t.RecordVerified(ctx, node.ID, report, verifier)
}
