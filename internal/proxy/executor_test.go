package proxy

import (
	"context"
	"path/filepath"
	"testing"
)

type toolFunc func(context.Context, Action) (Outcome, error)

func (f toolFunc) Call(ctx context.Context, a Action) (Outcome, error) { return f(ctx, a) }
func TestGovernedExecutionAndDenials(t *testing.T) {
	e, cap := setup(t)
	m := &MockTool{}
	req := Request{Key: "search", Verb: "paid_tool", Resource: "paid-search", Units: 1, DataClass: "public"}
	a, err := e.Execute(context.Background(), cap, req, m)
	if err != nil || a.Outcome == nil || a.Outcome.Cost != 375 {
		t.Fatal(a, err)
	}
	if _, err = e.Execute(context.Background(), cap, req, m); err != nil || m.Calls() != 1 {
		t.Fatal("retry", err)
	}
	req.Key = "flag"
	req.Resource = "paid-flagged"
	a, err = e.Execute(context.Background(), cap, req, m)
	if err != nil || a.State != "denied" || m.Calls() != 1 {
		t.Fatal("denied call", a, err)
	}
	e.Store.Close()
	if _, err = e.Execute(context.Background(), cap, request("closed"), m); err == nil || m.Calls() != 1 {
		t.Fatal("ledger fail open")
	}
}
func TestUnknownOutcomeKeepsHold(t *testing.T) {
	e, cap := setup(t)
	m := &MockTool{Uncertain: true}
	req := Request{Key: "unknown", Verb: "paid_tool", Resource: "paid-search", Units: 1, DataClass: "public"}
	if _, err := e.Execute(context.Background(), cap, req, m); err == nil {
		t.Fatal("uncertain hidden")
	}
	a, err := e.Execute(context.Background(), cap, req, m)
	if err != nil || a.State != "executing" || m.Calls() != 1 {
		t.Fatal(a, err)
	}
	r, _, _ := e.Inspect(cap)
	if r.Held != 500 || r.Spent != 0 {
		t.Fatal(r)
	}
}
func TestSettlementOutageSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(s)
	_, cap, err := e.Create(RunRequest{Budget: DefaultBudget(), Scope: []string{"paid_tool"}, Region: "us"}, "")
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Key: "crash", Verb: "paid_tool", Resource: "paid-search", Units: 1, DataClass: "public"}
	calls := 0
	tool := toolFunc(func(_ context.Context, a Action) (Outcome, error) {
		calls++
		s.Close()
		return Outcome{Cost: a.Reserved, Success: true}, nil
	})
	if _, err = e.Execute(context.Background(), cap, req, tool); err == nil {
		t.Fatal("settlement failure hidden")
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e = NewEngine(s)
	a, err := e.Execute(context.Background(), cap, req, tool)
	if err != nil || a.State != "executing" || calls != 1 {
		t.Fatal(a, err)
	}
	r, _, _ := e.Inspect(cap)
	if r.Held != 500 {
		t.Fatal("lost reservation")
	}
}
func TestPartialWriteFailureTripwire(t *testing.T) {
	e, cap := setup(t)
	m := &MockTool{Fail: true}
	e.Policy.SecondKey = false
	for _, key := range []string{"a", "b"} {
		req := Request{Key: key, Verb: "delete", Resource: "delete", Units: 1, DataClass: "internal"}
		a, err := e.Execute(context.Background(), cap, req, m)
		if err != nil || a.Outcome == nil || a.Outcome.Cost != 50 {
			t.Fatal(a, err)
		}
	}
	req := Request{Key: "c", Verb: "delete", Resource: "delete", Units: 1, DataClass: "internal"}
	a, err := e.Execute(context.Background(), cap, req, m)
	if err != nil || a.Decision.Rule != "failed_write_tripwire" || m.Calls() != 2 {
		t.Fatal(a, err)
	}
	if _, err = e.Execute(context.Background(), cap, request("read"), m); err != nil || m.Calls() != 3 {
		t.Fatal("read work blocked", err)
	}
}
