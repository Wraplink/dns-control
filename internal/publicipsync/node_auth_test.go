package publicipsync

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNodeAuthenticator(t *testing.T) {
	tokenA := strings.Repeat("a", 48)
	tokenB := strings.Repeat("b", 48)
	auth, err := NewNodeAuthenticator(map[string]string{"edge-eu-01": tokenA, "edge-eu-02": tokenB})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		headers []string
		want string
	}{
		{"first", []string{"Bearer " + tokenA}, "edge-eu-01"},
		{"second", []string{"Bearer " + tokenB}, "edge-eu-02"},
		{"missing", nil, ""},
		{"wrong", []string{"Bearer " + strings.Repeat("c", 48)}, ""},
		{"duplicate", []string{"Bearer " + tokenA, "Bearer " + tokenB}, ""},
		{"malformed", []string{tokenA}, ""},
		{"extra space", []string{"Bearer  " + tokenA}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			for _, header := range tc.headers {
				r.Header.Add("Authorization", header)
			}
			node, err := auth.Authenticate(r)
			if tc.want == "" {
				if !errors.Is(err, ErrInvalidNodeCredentials) {
					t.Fatalf("expected rejection, got node=%+v err=%v", node, err)
				}
			} else if err != nil || node.ID != tc.want {
				t.Fatalf("node=%+v err=%v, want %s", node, err, tc.want)
			}
		})
	}
}

func TestNodeAuthenticatorRejectsUnsafeConfiguration(t *testing.T) {
	secret := strings.Repeat("s", 48)
	for _, credentials := range []map[string]string{
		nil,
		{},
		{"": secret},
		{"edge": "short"},
		{"edge-a": secret, "edge-b": secret},
		{" edge": secret},
	} {
		if _, err := NewNodeAuthenticator(credentials); !errors.Is(err, ErrInvalidNodeCredentials) {
			t.Fatalf("accepted invalid configuration: %v", credentials)
		}
	}
}
