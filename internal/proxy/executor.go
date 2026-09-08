package proxy

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Tool interface {
	Call(context.Context, Action) (Outcome, error)
}
type MockTool struct {
	mu        sync.Mutex
	calls     map[string]int
	Fail      bool
	Uncertain bool
}

func (m *MockTool) Call(ctx context.Context, a Action) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.calls == nil {
		m.calls = map[string]int{}
	}
	m.calls[a.ID]++
	if m.calls[a.ID] > 1 {
		return Outcome{}, fmt.Errorf("duplicate mock dispatch")
	}
	if m.Uncertain {
		return Outcome{}, fmt.Errorf("simulated unknown outcome after dispatch")
	}
	out := Outcome{Cost: a.Reserved * 3 / 4, Tokens: a.ReservedTokens * 3 / 4, Effects: a.ReservedWrites, Success: true, Detail: "synthetic bounded result; no external side effects"}
	if m.Fail {
		out.Success = false
		out.Cost = a.Reserved / 2
		out.Effects = a.ReservedWrites / 2
		out.Detail = "known partial failure charged"
	}
	return out, nil
}
func (m *MockTool) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, v := range m.calls {
		n += v
	}
	return n
}
func (e *Engine) Execute(ctx context.Context, cap string, req Request, tool Tool) (Action, error) {
	a, fresh, err := e.Begin(cap, req)
	if err != nil || !fresh {
		return a, err
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	start := time.Now()
	out, err := tool.Call(bounded, a)
	if err != nil {
		return a, fmt.Errorf("outcome uncertain; reservation retained: %w", err)
	}
	out.LatencyMS = time.Since(start).Milliseconds()
	settled, err := e.settle(a.RunID, a.Request.Key, out)
	if err != nil {
		return a, fmt.Errorf("settlement unavailable; do not redispatch: %w", err)
	}
	return settled, nil
}
