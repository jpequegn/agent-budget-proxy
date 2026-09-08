package proxy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Comparison struct {
	Mode           string       `json:"mode"`
	Spent          int64        `json:"spent_micros"`
	Completed      int          `json:"completed"`
	Reads          int          `json:"successful_inspections"`
	Effects        int64        `json:"synthetic_write_effects"`
	Prevented      int64        `json:"prevented_write_effects_vs_caps"`
	Blocked        int          `json:"blocked_or_paused"`
	CostPerOutcome float64      `json:"micros_per_successful_outcome"`
	Replay         ReplayResult `json:"replay"`
}

func Experiment(out string) ([]Comparison, error) {
	if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(out, 0700); err != nil {
		return nil, err
	}
	results := []Comparison{}
	for _, mode := range []string{"request_caps", "steering", "second_key"} {
		result, err := experimentMode(filepath.Join(out, mode+".db"), mode)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	for i := range results {
		results[i].Prevented = results[0].Effects - results[i].Effects
	}
	if err := WriteJSON(filepath.Join(out, "comparison.json"), results); err != nil {
		return nil, err
	}
	markdown := "# Synthetic incident comparison\n\n| Mode | Spent micros | Successful outcomes | Inspections | Write effects | Prevented effects | Blocked/paused |\n| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n"
	for _, r := range results {
		markdown += fmt.Sprintf("| %s | %d | %d | %d | %d | %d | %d |\n", r.Mode, r.Spent, r.Completed, r.Reads, r.Effects, r.Prevented, r.Blocked)
	}
	markdown += "\nThis fixed mock incident is not a real model-quality or dollar-savings benchmark. Request caps use a large cumulative envelope; every request still has bounded units/tokens. Only the named single deletion is approved by the simulated reviewer. Paid/model success is a synthetic fixture outcome. Each ledger replays without invoking tools.\n"
	if err := os.WriteFile(filepath.Join(out, "comparison.md"), []byte(markdown), 0600); err != nil {
		return nil, err
	}
	return results, nil
}
func experimentMode(path, mode string) (Comparison, error) {
	s, err := Open(path)
	if err != nil {
		return Comparison{}, err
	}
	defer s.Close()
	e := NewEngine(s)
	now := int64(2000000)
	e.Clock = func() time.Time { return time.UnixMilli(now) }
	budget := DefaultBudget()
	if mode == "request_caps" {
		budget.Cost = 1e9
		budget.Tokens = 1e7
		budget.Writes = 1000
		e.Policy.Steer = false
		e.Policy.SecondKey = false
		e.Policy.DeletesPerHour = 1000
		e.Policy.FailureLimit = 100
	}
	if mode == "steering" {
		e.Policy.SecondKey = false
	}
	_, cap, err := e.Create(RunRequest{Budget: budget, Scope: []string{"inspect", "model", "delete", "paid_tool"}, Region: "us"}, "")
	if err != nil {
		return Comparison{}, err
	}
	requests := []Request{requestForExperiment("read-1", "inspect", "inspect", 1, 0)}
	for i := 0; i < 3; i++ {
		requests = append(requests, requestForExperiment(fmt.Sprintf("model-%d", i), "model", "model-premium", 1, 4000))
	}
	requests = append(requests, requestForExperiment("approved-delete", "delete", "delete", 1, 0))
	for i := 0; i < 3; i++ {
		requests = append(requests, requestForExperiment(fmt.Sprintf("broad-delete-%d", i), "delete", "delete", 50, 0))
	}
	requests = append(requests, requestForExperiment("flagged", "paid_tool", "paid-flagged", 1, 0), requestForExperiment("outage", "model", "model-premium", 1, 4000), requestForExperiment("read-2", "inspect", "inspect", 1, 0), requestForExperiment("read-3", "inspect", "inspect", 1, 0))
	mock := &MockTool{}
	for _, req := range requests {
		now += 1000
		if req.Key == "outage" {
			r := e.Policy.Resources["model-premium"]
			r.Available = false
			e.Policy.Resources[r.ID] = r
		}
		a, err := e.Execute(context.Background(), cap, req, mock)
		if err != nil {
			return Comparison{}, err
		}
		if req.Key == "approved-delete" && a.State == "awaiting_approval" {
			if _, err = e.Approve(a.ID, a.ApprovalDigest, "simulated-independent-reviewer"); err != nil {
				return Comparison{}, err
			}
			if _, err = e.Execute(context.Background(), cap, req, mock); err != nil {
				return Comparison{}, err
			}
		}
	}
	r, actions, err := e.Inspect(cap)
	if err != nil {
		return Comparison{}, err
	}
	result := Comparison{Mode: mode, Spent: r.Spent}
	for _, a := range actions {
		if a.Outcome != nil {
			result.Effects += a.Outcome.Effects
			if a.Outcome.Success {
				result.Completed++
				if a.Request.Verb == "inspect" {
					result.Reads++
				}
			}
		}
		if a.State == "denied" || a.State == "awaiting_approval" {
			result.Blocked++
		}
	}
	if result.Completed > 0 {
		result.CostPerOutcome = float64(result.Spent) / float64(result.Completed)
	}
	result.Replay, err = Replay(s)
	return result, err
}
func requestForExperiment(key, verb, res string, units, tokens int64) Request {
	return Request{Key: key, Verb: verb, Resource: res, Units: units, MaxTokens: tokens, DataClass: "public"}
}
