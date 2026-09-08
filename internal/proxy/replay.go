package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ReplayResult struct {
	Verified          bool   `json:"verified"`
	Decisions         int    `json:"decisions"`
	Runs              int    `json:"runs"`
	OriginalStateHash string `json:"original_state_hash"`
}

func comparable(a Action) string {
	return Hash(struct {
		State                string
		Effective            Request
		Decision             Decision
		Cost, Tokens, Writes int64
		Outcome              *Outcome
	}{
		a.State, a.Effective, a.Decision, a.Reserved, a.ReservedTokens, a.ReservedWrites, a.Outcome})
}

// Replay never invokes Tool.Call. It reruns policy/accounting and applies recorded outcomes.
func Replay(source *Store) (ReplayResult, error) {
	events, err := source.Events()
	if err != nil {
		return ReplayResult{}, err
	}
	final, err := source.View()
	if err != nil {
		return ReplayResult{}, err
	}
	root, err := os.MkdirTemp("", "abp-replay-")
	if err != nil {
		return ReplayResult{}, err
	}
	defer os.RemoveAll(root)
	store, err := Open(filepath.Join(root, "ledger.db"))
	if err != nil {
		return ReplayResult{}, err
	}
	defer store.Close()
	e := NewEngine(store)
	caps := map[string]string{}
	runs := map[string]string{}
	actions := map[string]Action{}
	count := 0
	for _, event := range events {
		at := event.At
		e.Clock = func() time.Time { return time.UnixMilli(at) }
		if event.Kind == "run.created" {
			if len(event.Runs) != 1 {
				return ReplayResult{}, fmt.Errorf("legacy or invalid creation event")
			}
			original := event.Runs[0]
			req := RunRequest{Budget: original.Budget, Scope: original.Scope, Region: original.Region, Parent: runs[original.Parent]}
			run, cap, err := e.Create(req, caps[original.Parent])
			if err != nil {
				return ReplayResult{}, err
			}
			runs[original.ID] = run.ID
			caps[original.ID] = cap
		}
		for _, original := range event.Actions {
			var current Action
			switch event.Kind {
			case "action.decide":
				if original.PolicySnapshot == nil || Hash(original.PolicySnapshot) != original.PolicyHash {
					return ReplayResult{}, fmt.Errorf("missing policy evidence")
				}
				e.Policy = *original.PolicySnapshot
				current, _, err = e.Begin(caps[original.RunID], original.Request)
				count++
			case "action.approve":
				prior, ok := actions[original.ID]
				if !ok {
					return ReplayResult{}, fmt.Errorf("approval lacks prior decision")
				}
				current, err = e.Approve(prior.ID, prior.ApprovalDigest, original.ApprovedBy)
			case "action.settle":
				if original.Outcome == nil {
					return ReplayResult{}, fmt.Errorf("settlement missing outcome")
				}
				current, err = e.settle(runs[original.RunID], original.Request.Key, *original.Outcome)
			default:
				return ReplayResult{}, fmt.Errorf("unsupported replay event %s", event.Kind)
			}
			if err != nil {
				return ReplayResult{}, err
			}
			if comparable(current) != comparable(original) {
				return ReplayResult{}, fmt.Errorf("replay decision mismatch for %s at %s", original.Request.Key, event.Kind)
			}
			actions[original.ID] = current
		}
	}
	replayed, err := store.View()
	if err != nil {
		return ReplayResult{}, err
	}
	if len(runs) != len(final.Runs) {
		return ReplayResult{}, fmt.Errorf("run history incomplete")
	}
	for oldID, newID := range runs {
		a, b := final.Runs[oldID], replayed.Runs[newID]
		if b == nil || a.Spent != b.Spent || a.Held != b.Held || a.Tokens != b.Tokens || a.HeldTokens != b.HeldTokens || a.Writes != b.Writes || a.Frozen != b.Frozen {
			return ReplayResult{}, fmt.Errorf("replayed balances differ")
		}
	}
	return ReplayResult{Verified: true, Decisions: count, Runs: len(runs), OriginalStateHash: Hash(final)}, nil
}
