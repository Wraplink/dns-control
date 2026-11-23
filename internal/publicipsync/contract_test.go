package publicipsync

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func fixture() (Operation, []string, []NodeReport) {
	op := Operation{SubscriptionID: uuid.New(), PublicIPID: uuid.New(), IPAddress: "8.8.8.8", Revision: 2, AttemptID: uuid.New()}
	nodes := []string{"edge-eu-01", "edge-eu-02"}
	reports := []NodeReport{
		{NodeID: nodes[0], SubscriptionID: op.SubscriptionID, PublicIPID: op.PublicIPID, Revision: op.Revision, AttemptID: op.AttemptID, Applied: true},
		{NodeID: nodes[1], SubscriptionID: op.SubscriptionID, PublicIPID: op.PublicIPID, Revision: op.Revision, AttemptID: op.AttemptID, Applied: true},
	}
	return op, nodes, reports
}

func TestValidateReports(t *testing.T) {
	op, nodes, reports := fixture()
	if err := ValidateReports(op, nodes, reports); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*Operation, *[]string, *[]NodeReport)
	}{
		{"missing", func(_ *Operation, _ *[]string, r *[]NodeReport) { *r = (*r)[:1] }},
		{"stale revision", func(_ *Operation, _ *[]string, r *[]NodeReport) { (*r)[0].Revision-- }},
		{"wrong attempt", func(_ *Operation, _ *[]string, r *[]NodeReport) { (*r)[0].AttemptID = uuid.New() }},
		{"wrong subscription", func(_ *Operation, _ *[]string, r *[]NodeReport) { (*r)[0].SubscriptionID = uuid.New() }},
		{"not applied", func(_ *Operation, _ *[]string, r *[]NodeReport) { (*r)[0].Applied = false }},
		{"duplicate report", func(_ *Operation, _ *[]string, r *[]NodeReport) { (*r)[1] = (*r)[0] }},
		{"unexpected node", func(_ *Operation, _ *[]string, r *[]NodeReport) { (*r)[0].NodeID = "unknown" }},
		{"duplicate required", func(_ *Operation, n *[]string, _ *[]NodeReport) { (*n)[1] = (*n)[0] }},
		{"empty required", func(_ *Operation, n *[]string, _ *[]NodeReport) { *n = nil }},
		{"invalid operation", func(o *Operation, _ *[]string, _ *[]NodeReport) { o.AttemptID = uuid.Nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, n, r := fixture()
			tc.mutate(&o, &n, &r)
			if err := ValidateReports(o, n, r); err == nil {
				t.Fatal("invalid reports accepted")
			}
		})
	}
}
func TestInvalidOperation(t *testing.T) {
	if err := (Operation{}).Validate(); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("got %v", err)
	}
}

func TestRejectNonPublicIPv4(t *testing.T) {
	for _, ip := range []string{"", "127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.1.2", "0.0.0.0", "::1", "2001:4860:4860::8888", "::ffff:8.8.8.8", "not-an-ip"} {
		t.Run(ip, func(t *testing.T) {
			op, _, _ := fixture()
			op.IPAddress = ip
			if !errors.Is(op.Validate(), ErrInvalidOperation) {
				t.Fatalf("accepted invalid IP %q", ip)
			}
		})
	}
}
