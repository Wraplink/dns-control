package publicipsync

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

var ErrInvalidNodeCredentials = errors.New("invalid edge agent credentials")

// NodeAuthenticator maps a credential to an authoritative node identity.
// The existing DNS Control shared agent bearer token cannot establish
// which individual node sent a report and MUST NOT be used here.
type NodeAuthenticator struct {
	// credentials maps authoritative node IDs to independent bearer tokens.
	// Configure from trusted server-side secrets, never from request data.
	credentials map[string]string
}

func NewNodeAuthenticator(credentials map[string]string) (*NodeAuthenticator, error) {
	if len(credentials) == 0 {
		return nil, ErrInvalidNodeCredentials
	}
	copyCredentials := make(map[string]string, len(credentials))
	seenTokens := make(map[string]struct{}, len(credentials))
	for nodeID, token := range credentials {
		if strings.TrimSpace(nodeID) != nodeID || nodeID == "" || strings.TrimSpace(token) != token || len(token) < 32 {
			return nil, ErrInvalidNodeCredentials
		}
		if _, duplicate := seenTokens[token]; duplicate {
			return nil, ErrInvalidNodeCredentials
		}
		seenTokens[token] = struct{}{}
		copyCredentials[nodeID] = token
	}
	return &NodeAuthenticator{credentials: copyCredentials}, nil
}

// Authenticate accepts exactly one RFC 6750-style Authorization bearer value.
// The node ID is returned from trusted server-side configuration, never a
// caller-provided node header or JSON field.
func (a *NodeAuthenticator) Authenticate(r *http.Request) (AuthenticatedNode, error) {
	if a == nil || len(a.credentials) == 0 {
		return AuthenticatedNode{}, ErrInvalidNodeCredentials
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return AuthenticatedNode{}, ErrInvalidNodeCredentials
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, " \t\r\n") {
		return AuthenticatedNode{}, ErrInvalidNodeCredentials
	}
	var matched string
	for id, secret := range a.credentials {
		if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1 {
			matched = id
		}
	}
	if matched == "" {
		return AuthenticatedNode{}, ErrInvalidNodeCredentials
	}
	return AuthenticatedNode{ID: matched}, nil
}
