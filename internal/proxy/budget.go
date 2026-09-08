package proxy

import "fmt"

type Quote struct {
	Cost, Tokens, Writes int64
	Effective            Request
	Decision             Decision
}

func refill(b Bucket, budget Budget, now int64) Bucket {
	if b.At == 0 {
		return Bucket{Milli: budget.Burst * 1000, At: now}
	}
	if now <= b.At {
		return b
	}
	elapsed := now - b.At
	if elapsed >= budget.Burst*1000/budget.Refill {
		b.Milli = budget.Burst * 1000
	} else {
		b.Milli += elapsed * budget.Refill
		if b.Milli > budget.Burst*1000 {
			b.Milli = budget.Burst * 1000
		}
	}
	b.At = now
	return b
}
func newAction(r *Run, req Request, now int64) *Action {
	return &Action{ID: ID(), RunID: r.ID, Agent: r.Agent, Session: r.Session, Request: req, Effective: req, Fingerprint: Hash(req), At: now}
}
func deny(a *Action, rule string, remaining int64) {
	a.State = "denied"
	a.Decision = Decision{Kind: "deny", Rule: rule, Reason: rule, Remaining: remaining}
}
func reserveState(s *State, r *Run, a *Action, q Quote, now int64) error {
	if q.Cost < 0 || q.Cost > 1e9 || q.Tokens < 0 || q.Tokens > 1e7 || q.Writes < 0 || q.Writes > 1000 {
		return fmt.Errorf("invalid trusted quote")
	}
	chain, err := ancestors(s, r)
	if err != nil {
		return err
	}
	for _, p := range chain {
		remaining := p.Budget.Cost - p.Spent - p.Held
		if p.Frozen {
			deny(a, "run_frozen", remaining)
			return nil
		}
		if now < p.LastClock || now < p.Created {
			deny(a, "clock_regression", remaining)
			return nil
		}
		if now >= p.Expires {
			deny(a, "wall_clock", remaining)
			return nil
		}
		if !contains(p.Scope, a.Request.Verb) {
			deny(a, "scope", remaining)
			return nil
		}
		if q.Cost > remaining {
			deny(a, "cost_budget", remaining)
			return nil
		}
		if q.Tokens > p.Budget.Tokens-p.Tokens-p.HeldTokens {
			deny(a, "token_budget", remaining)
			return nil
		}
		if q.Writes > p.Budget.Writes-p.Writes {
			deny(a, "write_budget", remaining)
			return nil
		}
		for _, key := range []string{"run", a.Request.Verb} {
			b := refill(p.Buckets[key], p.Budget, now)
			if b.Milli < 1000 {
				deny(a, "rate_limit", remaining)
				return nil
			}
		}
	}
	for _, p := range chain {
		p.Held += q.Cost
		p.HeldTokens += q.Tokens
		p.Writes += q.Writes
		p.LastClock = now
		for _, key := range []string{"run", a.Request.Verb} {
			b := refill(p.Buckets[key], p.Budget, now)
			b.Milli -= 1000
			p.Buckets[key] = b
		}
	}
	a.State = "executing"
	a.Reserved = q.Cost
	a.ReservedTokens = q.Tokens
	a.ReservedWrites = q.Writes
	a.Effective = q.Effective
	a.Decision = q.Decision
	if a.Decision.Kind == "" {
		a.Decision = Decision{Kind: "allow", Rule: "within_budget", Reason: "atomic reservation"}
	}
	a.Decision.Remaining = r.Budget.Cost - r.Spent - r.Held
	return nil
}

// reserve is an internal accounting boundary. Only trusted catalog code supplies quotes.
func (e *Engine) reserve(cap string, req Request, q Quote) (Action, bool, error) {
	if err := req.Validate(); err != nil {
		return Action{}, false, err
	}
	now := e.now()
	var out Action
	fresh := false
	err := e.Store.Update("action.reserve", now, func(s *State) error {
		r, err := authenticate(s, cap, now)
		if err != nil {
			return err
		}
		key := actionKey(r.ID, req.Key)
		if a := s.Actions[key]; a != nil {
			if a.Fingerprint != Hash(req) {
				return fmt.Errorf("idempotency payload conflict")
			}
			out = *a
			return nil
		}
		a := newAction(r, req, now)
		s.Actions[key] = a
		if err = reserveState(s, r, a, q, now); err != nil {
			return err
		}
		out = *a
		fresh = a.State == "executing"
		return nil
	})
	return out, fresh, err
}
func (e *Engine) settle(runID, key string, out Outcome) (Action, error) {
	if out.Cost < 0 || out.Cost > 1e12 || out.Tokens < 0 || out.Tokens > 1e9 || out.Effects < 0 || out.Effects > 1e6 || out.LatencyMS < 0 || len(out.Detail) > 500 {
		return Action{}, fmt.Errorf("invalid trusted outcome")
	}
	var result Action
	now := e.now()
	err := e.Store.Update("action.settle", now, func(s *State) error {
		a := s.Actions[actionKey(runID, key)]
		if a == nil {
			return fmt.Errorf("unknown action")
		}
		if a.State == "settled" || a.State == "overrun" {
			if Hash(a.Outcome) != Hash(out) {
				return fmt.Errorf("settlement conflict")
			}
			result = *a
			return nil
		}
		if a.State != "executing" {
			return fmt.Errorf("action not in flight")
		}
		chain, err := ancestors(s, s.Runs[runID])
		if err != nil {
			return err
		}
		over := out.Cost > a.Reserved || out.Tokens > a.ReservedTokens || out.Effects > a.ReservedWrites
		for _, p := range chain {
			p.Held -= a.Reserved
			p.HeldTokens -= a.ReservedTokens
			p.Spent += out.Cost
			p.Tokens += out.Tokens
			if !out.Success && a.ReservedWrites > 0 {
				p.FailedWrites++
			}
			if over {
				p.Frozen = true
			}
		}
		a.Outcome = &out
		a.SettledAt = now
		a.State = "settled"
		if over {
			a.State = "overrun"
		}
		result = *a
		return nil
	})
	return result, err
}
