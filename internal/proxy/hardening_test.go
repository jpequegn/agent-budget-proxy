package proxy

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEpochZeroBucketCannotReset(t *testing.T) {
	b := refill(Bucket{}, Budget{Burst: 1, Refill: 1}, 0)
	b.Milli -= 1000
	if refill(b, Budget{Burst: 1, Refill: 1}, 0).Milli != 0 {
		t.Fatal("epoch zero resets exhausted bucket")
	}
	if refill(b, Budget{Burst: 1, Refill: 1}, 500).Milli != 500 {
		t.Fatal("epoch zero refill")
	}
}

func TestLiveAuditCorruptionStopsDispatch(t *testing.T) {
	e, cap := setup(t)
	tool := &MockTool{}
	if _, err := e.Execute(context.Background(), cap, request("before"), tool); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.db.Exec("UPDATE audit SET body='{}' WHERE seq=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), cap, request("after"), tool); err == nil {
		t.Fatal("corrupt history accepted")
	}
	if tool.Calls() != 1 {
		t.Fatal("dispatch after corruption")
	}
	if _, err := e.Store.View(); err == nil {
		t.Fatal("view accepted corruption")
	}
	if _, err := e.Store.Events(); err == nil {
		t.Fatal("export accepted corruption")
	}
}

func TestSeparateConnectionsShareAtomicBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	e1 := NewEngine(s1)
	_, cap, err := e1.Create(RunRequest{Budget: DefaultBudget(), Scope: []string{"inspect"}, Region: "us"}, "")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	e2 := NewEngine(s2)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := []*Engine{e1, e2}[i%2]
			r := request(fmt.Sprint(i))
			if _, _, err := e.reserve(cap, r, quote(r, 10000)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	r, _, err := e1.Inspect(cap)
	if err != nil || r.Held != r.Budget.Cost {
		t.Fatal(r, err)
	}
}

func TestRandomBoundedSettlementConservesBudget(t *testing.T) {
	e, cap := setup(t)
	rng := rand.New(rand.NewSource(245))
	for i := 0; i < 60; i++ {
		e.Clock = func() time.Time { return time.Unix(1000+int64(i), 0) }
		req := request(fmt.Sprint(i))
		cost := rng.Int63n(20000)
		a, fresh, err := e.reserve(cap, req, quote(req, cost))
		if err != nil {
			t.Fatal(err)
		}
		if fresh && i%4 != 0 {
			if _, err = e.settle(a.RunID, req.Key, Outcome{Cost: rng.Int63n(cost + 1), Success: i%3 != 0}); err != nil {
				t.Fatal(err)
			}
		}
		r, actions, err := e.Inspect(cap)
		if err != nil {
			t.Fatal(err)
		}
		var held, spent int64
		for _, a := range actions {
			if a.State == "executing" {
				held += a.Reserved
			}
			if a.Outcome != nil {
				spent += a.Outcome.Cost
			}
		}
		if r.Held != held || r.Spent != spent || held+spent > r.Budget.Cost || held < 0 || spent < 0 {
			t.Fatal("accounting invariant", r)
		}
	}
}

func TestCollectorOutageAndSecretFreeTraces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	if err := WaitCollector(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
	if err := WaitCollector(context.Background(), ""); err == nil {
		t.Fatal("missing collector")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitCollector(canceled, "http://127.0.0.1:1"); err == nil {
		t.Fatal("readiness ignored cancellation")
	}
	var local bytes.Buffer
	ctx := context.Background()
	provider, err := Telemetry(ctx, server.URL, &local)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Shutdown(ctx)
	e, cap := setup(t)
	e.Tracer = provider.Tracer("outage-test")
	a, err := e.Execute(ctx, cap, request("collector-down"), &MockTool{})
	if err != nil || a.State != "settled" {
		t.Fatal(a, err)
	}
	if err := provider.ForceFlush(ctx); err == nil {
		t.Fatal("expected collector failure")
	}
	if strings.Contains(local.String(), cap) || !strings.Contains(local.String(), "ledger.settle") {
		t.Fatal("trace content")
	}
	if err := e.Store.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestRandomGovernedWritesStayInsideAncestorBudget(t *testing.T) {
	e, cap := setup(t)
	e.Policy.SecondKey = false
	e.Policy.DeletesPerHour = 1000
	e.Policy.FailureLimit = 100
	parent, _, err := e.Inspect(cap)
	if err != nil {
		t.Fatal(err)
	}
	_, childCap, err := e.Create(RunRequest{Parent: parent.ID, Budget: DefaultBudget(), Scope: []string{"delete"}, Region: "us"}, cap)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(245))
	tool := &MockTool{Fail: true}
	for i := 0; i < 50; i++ {
		e.Clock = func() time.Time { return time.Unix(1000+int64(i), 0) }
		req := Request{Key: fmt.Sprint(i), Verb: "delete", Resource: "delete", Units: 1 + rng.Int63n(4), DataClass: "internal"}
		authority := []string{cap, childCap}[i%2]
		if _, err := e.Execute(context.Background(), authority, req, tool); err != nil {
			t.Fatal(err)
		}
		r, _, err := e.Inspect(cap)
		if err != nil || r.Writes > r.Budget.Writes {
			t.Fatal(r, err)
		}
	}
	state, err := e.Store.View()
	if err != nil {
		t.Fatal(err)
	}
	var attempts, effects int64
	for _, a := range state.Actions {
		attempts += a.ReservedWrites
		if a.Outcome != nil {
			effects += a.Outcome.Effects
		}
	}
	r, _, _ := e.Inspect(cap)
	if attempts != r.Writes || effects > attempts || attempts == 0 || tool.Calls() == 0 {
		t.Fatal(attempts, effects, r)
	}
	if _, err := Replay(e.Store); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalExpiryNeverDispatches(t *testing.T) {
	e, cap := setup(t)
	req := Request{Key: "expired", Verb: "delete", Resource: "delete", Units: 1, DataClass: "internal"}
	a, _, err := e.Begin(cap, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Approve(a.ID, a.ApprovalDigest, "reviewer"); err != nil {
		t.Fatal(err)
	}
	e.Clock = func() time.Time { return time.UnixMilli(a.ApprovalUntil) }
	tool := &MockTool{}
	a, err = e.Execute(context.Background(), cap, req, tool)
	if err != nil || a.Decision.Rule != "approval_expired_or_changed" || tool.Calls() != 0 {
		t.Fatal(a, err)
	}
}

func TestUnavailableFallbackCannotExceedRoleBudget(t *testing.T) {
	for _, cost := range []int64{100, 1000, 100000} {
		t.Run(fmt.Sprint(cost), func(t *testing.T) {
			e, _ := setup(t)
			b := DefaultBudget()
			b.Cost = cost
			_, cap, err := e.Create(RunRequest{Budget: b, Scope: []string{"model"}, Region: "us"}, "")
			if err != nil {
				t.Fatal(err)
			}
			resource := e.Policy.Resources["model-premium"]
			resource.Available = false
			e.Policy.Resources[resource.ID] = resource
			req := Request{Key: "role", Verb: "model", Resource: "model-premium", Units: 1, MaxTokens: 6000, DataClass: "public"}
			a, err := e.Execute(context.Background(), cap, req, &MockTool{})
			if err != nil {
				t.Fatal(err)
			}
			r, _, err := e.Inspect(cap)
			if err != nil || r.Spent+r.Held > cost {
				t.Fatal(r, err)
			}
			if cost == 100 && a.State != "denied" {
				t.Fatal("unaffordable fallback", a)
			}
			if cost > 100 && (a.State != "settled" || a.Effective.Resource != "model-small") {
				t.Fatal(a)
			}
		})
	}
}
