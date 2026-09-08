package proxy

import "fmt"

type Resource struct {
	ID         string `json:"id"`
	Verb       string `json:"verb"`
	Region     string `json:"region"`
	DataClass  string `json:"data_class"`
	UnitPrice  int64  `json:"unit_price_micros"`
	TokenPrice int64  `json:"token_price_micros"`
	MaxUnits   int64  `json:"max_units"`
	Available  bool   `json:"available"`
	Reversible bool   `json:"reversible"`
	Flagged    bool   `json:"flagged"`
	HighRisk   bool   `json:"high_risk"`
	Fallback   string `json:"fallback,omitempty"`
}
type Policy struct {
	Resources      map[string]Resource `json:"resources"`
	Steer          bool                `json:"steer"`
	SecondKey      bool                `json:"second_key"`
	DeletesPerHour int64               `json:"deletes_per_hour"`
	FailureLimit   int64               `json:"failure_limit"`
	ApprovalCost   int64               `json:"approval_cost_micros"`
}

func DefaultPolicy() Policy {
	resources := []Resource{
		{ID: "inspect", Verb: "inspect", Region: "us", DataClass: "internal", MaxUnits: 1, Available: true, Reversible: true},
		{ID: "delete", Verb: "delete", Region: "us", DataClass: "internal", UnitPrice: 100, MaxUnits: 100, Available: true, HighRisk: true},
		{ID: "model-premium", Verb: "model", Region: "us", DataClass: "internal", TokenPrice: 20, MaxUnits: 1, Available: true, Reversible: true, Fallback: "model-small"},
		{ID: "model-small", Verb: "model", Region: "us", DataClass: "internal", TokenPrice: 2, MaxUnits: 1, Available: true, Reversible: true},
		{ID: "paid-search", Verb: "paid_tool", Region: "us", DataClass: "public", UnitPrice: 500, MaxUnits: 10, Available: true, Reversible: true},
		{ID: "paid-archive", Verb: "paid_tool", Region: "eu", DataClass: "internal", UnitPrice: 700, MaxUnits: 10, Available: true, Reversible: true},
		{ID: "paid-flagged", Verb: "paid_tool", Region: "us", DataClass: "public", UnitPrice: 50, MaxUnits: 10, Available: true, Reversible: true, Flagged: true},
		{ID: "db-small", Verb: "provision", Region: "us", DataClass: "internal", UnitPrice: 20000, MaxUnits: 1, Available: true, HighRisk: true},
		{ID: "compute-small", Verb: "provision", Region: "us", DataClass: "internal", UnitPrice: 40000, MaxUnits: 1, Available: true, HighRisk: true},
		{ID: "storage-eu", Verb: "provision", Region: "eu", DataClass: "internal", UnitPrice: 10000, MaxUnits: 1, Available: true, HighRisk: true},
	}
	p := Policy{Resources: map[string]Resource{}, Steer: true, SecondKey: true, DeletesPerHour: 3, FailureLimit: 2, ApprovalCost: 20000}
	for _, r := range resources {
		p.Resources[r.ID] = r
	}
	return p
}
func (p Policy) Validate() error {
	if len(p.Resources) == 0 || len(p.Resources) > 30 || p.DeletesPerHour < 0 || p.DeletesPerHour > 1000 || p.FailureLimit < 1 || p.FailureLimit > 100 || p.ApprovalCost < 1 || p.ApprovalCost > 1e9 {
		return fmt.Errorf("invalid policy")
	}
	for id, r := range p.Resources {
		if id != r.ID || !SafeName(id) || !validateScope([]string{r.Verb}) || (r.Region != "us" && r.Region != "eu") ||
			(r.DataClass != "public" && r.DataClass != "internal") || r.UnitPrice < 0 || r.UnitPrice > 1e6 || r.TokenPrice < 0 || r.TokenPrice > 100 || r.MaxUnits < 1 || r.MaxUnits > 1000 {
			return fmt.Errorf("invalid resource")
		}
	}
	return nil
}
func eligible(resource Resource, run *Run, req Request) string {
	if resource.Verb != req.Verb {
		return "verb_mismatch"
	}
	if resource.Flagged {
		return "counterparty_hold"
	}
	if resource.Region != run.Region {
		return "locality"
	}
	if req.DataClass == "internal" && resource.DataClass != "internal" {
		return "data_class"
	}
	if req.Units > resource.MaxUnits {
		return "resource_quota"
	}
	return ""
}
func (p Policy) evaluate(chain []*Run, req Request) Quote {
	q := Quote{Effective: req, Decision: Decision{Kind: "allow", Rule: "catalog_allowed", Reason: "catalog identity, locality and data class passed"}}
	block := func(rule string) Quote { q.Decision = Decision{Kind: "deny", Rule: rule, Reason: rule}; return q }
	r := chain[0]
	remaining := r.Budget.Cost - r.Spent - r.Held
	for _, a := range chain {
		v := a.Budget.Cost - a.Spent - a.Held
		if v < remaining {
			remaining = v
		}
		if !contains(a.Scope, req.Verb) {
			return block("scope")
		}
	}
	res, ok := p.Resources[req.Resource]
	if !ok {
		return block("unknown_resource")
	}
	if rule := eligible(res, r, req); rule != "" {
		return block(rule)
	}
	if req.Verb == "model" && req.MaxTokens < 1 {
		return block("missing_token_bound")
	}
	if req.Verb != "model" && req.MaxTokens != 0 {
		return block("unexpected_tokens")
	}
	price := func(x Resource) int64 { return x.UnitPrice*q.Effective.Units + x.TokenPrice*q.Effective.MaxTokens }
	if p.Steer && (!res.Available || price(res) > remaining) && res.Fallback != "" {
		fallback, ok := p.Resources[res.Fallback]
		if ok && eligible(fallback, r, req) == "" && fallback.Available {
			res = fallback
			q.Effective.Resource = res.ID
			q.Decision.Steering = append(q.Decision.Steering, "switch_model")
		}
	}
	if !res.Available {
		return block("provider_unavailable")
	}
	if p.Steer && req.Verb == "model" && (remaining < r.Budget.Cost/2 || price(res) > remaining) && q.Effective.MaxTokens > 200 {
		q.Effective.MaxTokens = 200
		q.Decision.Steering = append(q.Decision.Steering, "compact_context", "request_brevity", "bounded_instruction: return only requested fields")
	}
	if p.Steer && req.Verb == "paid_tool" && remaining < r.Budget.Cost/2 && q.Effective.Units > 2 {
		q.Effective.Units = 2
		q.Decision.Steering = append(q.Decision.Steering, "reduce_retrieval_output")
	}
	q.Cost = price(res)
	q.Tokens = q.Effective.MaxTokens
	if req.Verb == "delete" || req.Verb == "provision" {
		q.Writes = q.Effective.Units
	}
	for _, a := range chain {
		if q.Writes > 0 && a.FailedWrites >= p.FailureLimit {
			return block("failed_write_tripwire")
		}
		if req.Verb == "delete" && q.Writes > p.DeletesPerHour-a.Deletes {
			return block("delete_tripwire")
		}
	}
	q.Decision.Remaining = remaining
	if len(q.Decision.Steering) > 0 {
		q.Decision.Kind = "steer"
		q.Decision.Rule = "budget_steering"
		q.Decision.Reason = "bounded request applied to selected mock provider"
	}
	if p.SecondKey && (!res.Reversible || res.HighRisk || q.Cost >= p.ApprovalCost) {
		q.Decision.Kind = "approval"
		q.Decision.Rule = "second_key"
		q.Decision.Reason = "irreversible, high-risk or unusual spend"
	}
	return q
}
func approvalDigest(a *Action, q Quote, policy Policy) string {
	return Hash(struct {
		ID, Fingerprint, Policy string
		Cost, Tokens, Writes    int64
		Effective               Request
	}{a.ID, a.Fingerprint, Hash(policy), q.Cost, q.Tokens, q.Writes, q.Effective})
}
func (e *Engine) Begin(cap string, req Request) (Action, bool, error) {
	if err := req.Validate(); err != nil {
		return Action{}, false, err
	}
	if err := e.Policy.Validate(); err != nil {
		return Action{}, false, err
	}
	now := e.now()
	var result Action
	fresh := false
	err := e.Store.Update("action.decide", now, func(s *State) error {
		r, err := authenticate(s, cap, now)
		if err != nil {
			return err
		}
		key := actionKey(r.ID, req.Key)
		a := s.Actions[key]
		if a != nil {
			if a.Fingerprint != Hash(req) {
				return fmt.Errorf("idempotency payload conflict")
			}
			if a.State != "awaiting_approval" || a.ApprovedBy == "" {
				result = *a
				return nil
			}
		} else {
			a = newAction(r, req, now)
			s.Actions[key] = a
		}
		chain, err := ancestors(s, r)
		if err != nil {
			return err
		}
		for _, p := range chain {
			if now >= p.Window+3600000 {
				p.Window = now
				p.Deletes = 0
				p.FailedWrites = 0
			}
		}
		q := e.Policy.evaluate(chain, req)
		a.Effective = q.Effective
		a.EstimatedCost = q.Cost
		a.EstimatedTokens = q.Tokens
		a.Resource = e.Policy.Resources[q.Effective.Resource]
		if q.Decision.Kind == "deny" {
			a.State = "denied"
			a.Decision = q.Decision
			result = *a
			return nil
		}
		if a.ApprovedBy != "" && (now >= a.ApprovalUntil || a.ApprovalDigest != approvalDigest(a, q, e.Policy)) {
			deny(a, "approval_expired_or_changed", q.Decision.Remaining)
			result = *a
			return nil
		}
		if q.Decision.Kind == "approval" && a.ApprovedBy == "" {
			a.State = "awaiting_approval"
			a.Decision = q.Decision
			a.ApprovalDigest = approvalDigest(a, q, e.Policy)
			a.PolicyHash = Hash(e.Policy)
			a.ApprovalUntil = now + 120000
			if a.ApprovalUntil > r.Expires {
				a.ApprovalUntil = r.Expires
			}
			result = *a
			return nil
		}
		if q.Decision.Kind == "approval" {
			q.Decision.Kind = "allow"
			q.Decision.Reason = "independent second key verified; budgets rechecked"
		}
		a.PolicyHash = Hash(e.Policy)
		a.Resource = e.Policy.Resources[q.Effective.Resource]
		if err = reserveState(s, r, a, q, now); err != nil {
			return err
		}
		if a.State == "executing" {
			fresh = true
			for _, p := range chain {
				if req.Verb == "delete" {
					p.Deletes += q.Writes
				}
			}
		}
		result = *a
		return nil
	})
	return result, fresh, err
}
func (e *Engine) Approve(actionID, digest, reviewer string) (Action, error) {
	if !SafeName(reviewer) {
		return Action{}, fmt.Errorf("invalid reviewer")
	}
	now := e.now()
	var result Action
	err := e.Store.Update("action.approve", now, func(s *State) error {
		for _, a := range s.Actions {
			if a.ID != actionID {
				continue
			}
			if a.State != "awaiting_approval" || a.ApprovedBy != "" || a.Agent == reviewer || a.Session == reviewer {
				return fmt.Errorf("independent pending approval required")
			}
			if a.ApprovalDigest != digest || now < a.At || now >= a.ApprovalUntil {
				return fmt.Errorf("approval mismatch or expiry")
			}
			a.ApprovedBy = reviewer
			result = *a
			return nil
		}
		return fmt.Errorf("unknown action")
	})
	return result, err
}
