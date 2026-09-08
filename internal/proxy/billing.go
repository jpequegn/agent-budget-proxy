package proxy

import (
	"fmt"
	"regexp"
	"sort"
	"time"
)

// BillingEvent matches metered-billing-sandbox UsageEvent. Cost is metadata,
// never a quantity of cents; downstream pricing remains an explicit decision.
type BillingEvent struct {
	ID       string         `json:"event_id"`
	Key      string         `json:"idempotency_key"`
	Customer string         `json:"customer_id"`
	Product  string         `json:"product_id"`
	Quantity int            `json:"quantity"`
	At       string         `json:"occurred_at"`
	Agent    string         `json:"agent_id"`
	Workflow string         `json:"workflow_id"`
	Risk     string         `json:"risk_level"`
	Metadata map[string]any `json:"metadata"`
}

func (s *Store) BillingEvents(customer string) ([]BillingEvent, error) {
	if !regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`).MatchString(customer) {
		return nil, fmt.Errorf("customer must be a billing sandbox identifier")
	}
	state, err := s.View()
	if err != nil {
		return nil, err
	}
	events := []BillingEvent{}
	for _, a := range state.Actions {
		if a.Outcome == nil || (a.State != "settled" && a.State != "overrun") {
			continue
		}
		risk := "low"
		if a.Resource.HighRisk || !a.Resource.Reversible {
			risk = "high"
		}
		events = append(events, BillingEvent{
			ID: "abp_" + a.ID, Key: "abp:" + a.ID, Customer: customer,
			Product: "abp_" + a.Effective.Verb, Quantity: 1,
			At:    time.UnixMilli(a.SettledAt).UTC().Format(time.RFC3339Nano),
			Agent: "agent_" + a.Agent, Workflow: "run_" + a.RunID, Risk: risk,
			Metadata: map[string]any{"synthetic": true, "action_id": a.ID, "resource": a.Effective.Resource, "cost_micros": a.Outcome.Cost, "tokens": a.Outcome.Tokens, "effects": a.Outcome.Effects, "success": a.Outcome.Success, "state": a.State, "policy_hash": a.PolicyHash},
		})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].ID < events[j].ID })
	return events, nil
}
