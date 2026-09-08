package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Report struct {
	Runs        []Run    `json:"runs"`
	Actions     []Action `json:"actions"`
	AuditEvents int      `json:"audit_events"`
}

func (s *Store) Report() (Report, error) {
	if err := s.Verify(); err != nil {
		return Report{}, err
	}
	state, err := s.View()
	if err != nil {
		return Report{}, err
	}
	events, err := s.Events()
	if err != nil {
		return Report{}, err
	}
	r := Report{Runs: []Run{}, Actions: []Action{}, AuditEvents: len(events)}
	for _, run := range state.Runs {
		r.Runs = append(r.Runs, *run)
	}
	for _, a := range state.Actions {
		r.Actions = append(r.Actions, *a)
	}
	sort.Slice(r.Runs, func(i, j int) bool { return r.Runs[i].ID < r.Runs[j].ID })
	sort.Slice(r.Actions, func(i, j int) bool {
		if r.Actions[i].At == r.Actions[j].At {
			return r.Actions[i].ID < r.Actions[j].ID
		}
		return r.Actions[i].At < r.Actions[j].At
	})
	return r, nil
}
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
func Demo(out string) (Report, error) {
	return DemoWithEndpoint(out, "")
}
func DemoWithEndpoint(out, endpoint string) (Report, error) {
	if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		return Report{}, err
	}
	if err := os.Mkdir(out, 0700); err != nil {
		return Report{}, err
	}
	s, err := Open(filepath.Join(out, "ledger.db"))
	if err != nil {
		return Report{}, err
	}
	defer s.Close()
	e := NewEngine(s)
	traceFile, err := os.OpenFile(filepath.Join(out, "traces.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Report{}, err
	}
	defer traceFile.Close()
	provider, err := Telemetry(context.Background(), endpoint, traceFile)
	if err != nil {
		return Report{}, err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		provider.Shutdown(ctx)
	}()
	e.Tracer = provider.Tracer("agent-budget-proxy")
	_, cap, err := e.Create(RunRequest{Budget: DefaultBudget(), Scope: []string{"inspect", "model", "delete", "paid_tool", "provision"}, Region: "us"}, "")
	if err != nil {
		return Report{}, err
	}
	mock := &MockTool{}
	requests := []Request{
		{Key: "read", Verb: "inspect", Resource: "inspect", Units: 1, DataClass: "public"},
		{Key: "model", Verb: "model", Resource: "model-premium", Units: 1, MaxTokens: 6000, DataClass: "public"},
		{Key: "one-delete", Verb: "delete", Resource: "delete", Units: 1, DataClass: "internal"},
		{Key: "storm", Verb: "delete", Resource: "delete", Units: 100, DataClass: "internal"},
		{Key: "flagged", Verb: "paid_tool", Resource: "paid-flagged", Units: 1, DataClass: "public"},
	}
	for _, req := range requests {
		a, err := e.Execute(context.Background(), cap, req, mock)
		if err != nil {
			return Report{}, err
		}
		if a.State == "awaiting_approval" {
			if _, err = e.Approve(a.ID, a.ApprovalDigest, "demo-independent-reviewer"); err != nil {
				return Report{}, err
			}
			if _, err = e.Execute(context.Background(), cap, req, mock); err != nil {
				return Report{}, err
			}
		}
	}
	report, err := s.Report()
	if err != nil {
		return report, err
	}
	if err = WriteJSON(filepath.Join(out, "report.json"), report); err != nil {
		return report, err
	}
	return report, nil
}
func (r Report) Summary() string {
	spent, held := int64(0), int64(0)
	completed, blocked := 0, 0
	for _, run := range r.Runs {
		if run.Parent == "" {
			spent += run.Spent
			held += run.Held
		}
	}
	for _, a := range r.Actions {
		if a.Outcome != nil && a.Outcome.Success {
			completed++
		}
		if a.State == "denied" || a.State == "awaiting_approval" {
			blocked++
		}
	}
	return fmt.Sprintf("spent=%d micros held=%d micros completed=%d blocked_or_paused=%d", spent, held, completed, blocked)
}
