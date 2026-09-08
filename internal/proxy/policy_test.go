package proxy

import "testing"

func TestPolicyGatesAndApproval(t *testing.T) {
	e, cap := setup(t)
	for _, tc := range []struct {
		req  Request
		rule string
	}{
		{Request{Key: "flag", Verb: "paid_tool", Resource: "paid-flagged", Units: 1, DataClass: "public"}, "counterparty_hold"},
		{Request{Key: "eu", Verb: "provision", Resource: "storage-eu", Units: 1, DataClass: "public"}, "locality"},
		{Request{Key: "class", Verb: "paid_tool", Resource: "paid-search", Units: 1, DataClass: "internal"}, "data_class"},
		{Request{Key: "storm", Verb: "delete", Resource: "delete", Units: 50, DataClass: "internal"}, "delete_tripwire"},
	} {
		a, fresh, err := e.Begin(cap, tc.req)
		if err != nil || fresh || a.Decision.Rule != tc.rule {
			t.Fatal(a, err)
		}
	}
	req := Request{Key: "delete", Verb: "delete", Resource: "delete", Units: 1, DataClass: "internal"}
	a, fresh, err := e.Begin(cap, req)
	if err != nil || fresh || a.State != "awaiting_approval" {
		t.Fatal(a, err)
	}
	if _, err = e.Approve(a.ID, a.ApprovalDigest, a.Agent); err == nil {
		t.Fatal("self approval")
	}
	if _, err = e.Approve(a.ID, "bad", "reviewer"); err == nil {
		t.Fatal("wrong digest")
	}
	if _, err = e.Approve(a.ID, a.ApprovalDigest, "reviewer"); err != nil {
		t.Fatal(err)
	}
	a, fresh, err = e.Begin(cap, req)
	if err != nil || !fresh || a.State != "executing" {
		t.Fatal(a, err)
	}
}
func TestFallbackWithinRemainingBudget(t *testing.T) {
	e, cap := setup(t)
	req := Request{Key: "model", Verb: "model", Resource: "model-premium", Units: 1, MaxTokens: 6000, DataClass: "public"}
	a, fresh, err := e.Begin(cap, req)
	if err != nil || !fresh || a.Effective.Resource != "model-small" || a.Reserved > 100000 {
		t.Fatal(a, err)
	}
	e, cap = setup(t)
	r := e.Policy.Resources["model-premium"]
	r.Available = false
	e.Policy.Resources[r.ID] = r
	a, fresh, err = e.Begin(cap, req)
	if err != nil || !fresh || a.Effective.Resource != "model-small" {
		t.Fatal(a, err)
	}
}
func TestChangedQuoteInvalidatesApproval(t *testing.T) {
	e, cap := setup(t)
	req := Request{Key: "db", Verb: "provision", Resource: "db-small", Units: 1, DataClass: "internal"}
	a, _, _ := e.Begin(cap, req)
	if _, err := e.Approve(a.ID, a.ApprovalDigest, "reviewer"); err != nil {
		t.Fatal(err)
	}
	resource := e.Policy.Resources["db-small"]
	resource.UnitPrice++
	e.Policy.Resources[resource.ID] = resource
	a, fresh, err := e.Begin(cap, req)
	if err != nil || fresh || a.Decision.Rule != "approval_expired_or_changed" {
		t.Fatal(a, err)
	}
}
