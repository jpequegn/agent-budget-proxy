package proxy

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func request(key string) Request {
	return Request{Key: key, Verb: "inspect", Resource: "inspect", Units: 1, DataClass: "public"}
}
func quote(req Request, cost int64) Quote { return Quote{Cost: cost, Effective: req} }
func TestConcurrentReservations(t *testing.T) {
	e, cap := setup(t)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := request(fmt.Sprint(i))
			if _, _, err := e.reserve(cap, req, quote(req, 10000)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	run, actions, err := e.Inspect(cap)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, a := range actions {
		if a.State == "executing" {
			count++
		}
	}
	if run.Held != 100000 || count != 10 {
		t.Fatal(run.Held, count)
	}
}
func TestIdempotencyAndFailedSpend(t *testing.T) {
	e, cap := setup(t)
	req := request("one")
	a, fresh, err := e.reserve(cap, req, quote(req, 100))
	if err != nil || !fresh {
		t.Fatal(err)
	}
	b, fresh, err := e.reserve(cap, req, quote(req, 100))
	if err != nil || fresh || b.ID != a.ID {
		t.Fatal("duplicate", err)
	}
	changed := req
	changed.Units = 2
	if _, _, err = e.reserve(cap, changed, quote(changed, 100)); err == nil {
		t.Fatal("payload conflict")
	}
	out := Outcome{Cost: 70, Success: false, Detail: "provider failure"}
	if _, err = e.settle(a.RunID, req.Key, out); err != nil {
		t.Fatal(err)
	}
	if _, err = e.settle(a.RunID, req.Key, out); err != nil {
		t.Fatal(err)
	}
	r, _, _ := e.Inspect(cap)
	if r.Spent != 70 || r.Held != 0 {
		t.Fatal(r)
	}
}
func TestAncestorsAndRates(t *testing.T) {
	e, cap := setup(t)
	parent, _, _ := e.Inspect(cap)
	_, childCap, err := e.Create(RunRequest{Parent: parent.ID, Budget: DefaultBudget(), Scope: []string{"inspect"}, Region: "us"}, cap)
	if err != nil {
		t.Fatal(err)
	}
	req := request("child")
	if _, _, err = e.reserve(childCap, req, quote(req, 90000)); err != nil {
		t.Fatal(err)
	}
	req = request("parent")
	a, _, err := e.reserve(cap, req, quote(req, 20000))
	if err != nil || a.Decision.Rule != "cost_budget" {
		t.Fatal(a, err)
	}
	b := Budget{Burst: 1, Refill: 1}
	bucket := refill(Bucket{}, b, 1000)
	bucket.Milli -= 1000
	if refill(bucket, b, 900).Milli != 0 || refill(bucket, b, 1500).Milli != 500 || refill(bucket, b, 2000).Milli != 1000 {
		t.Fatal("refill")
	}
}
func TestClockAndOverrun(t *testing.T) {
	e, cap := setup(t)
	req := request("one")
	a, _, err := e.reserve(cap, req, quote(req, 100))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.settle(a.RunID, req.Key, Outcome{Cost: 101}); err != nil {
		t.Fatal(err)
	}
	r, _, _ := e.Inspect(cap)
	if !r.Frozen || r.Spent != 101 {
		t.Fatal("overrun hidden")
	}
	e, cap = setup(t)
	req = request("two")
	e.reserve(cap, req, quote(req, 1))
	e.Clock = func() time.Time { return time.Unix(999, 0) }
	if _, _, err = e.reserve(cap, request("three"), quote(request("three"), 1)); err == nil {
		t.Fatal("backward clock accepted")
	}
}
