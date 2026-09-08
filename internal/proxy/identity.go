package proxy

import (
	"fmt"
	"time"
)

type Engine struct {
	Store *Store
	Clock func() time.Time
}

func NewEngine(s *Store) *Engine { return &Engine{Store: s, Clock: time.Now} }
func (e *Engine) now() int64     { return e.Clock().UnixMilli() }
func authenticate(s *State, cap string, now int64) (*Run, error) {
	id, ok := s.Capabilities[Digest([]byte(cap))]
	if !ok {
		return nil, fmt.Errorf("invalid capability")
	}
	run := s.Runs[id]
	if run == nil || now < run.Created || now >= run.Expires {
		return nil, fmt.Errorf("capability expired or invalid clock")
	}
	return run, nil
}
func ancestors(s *State, r *Run) ([]*Run, error) {
	chain := []*Run{}
	seen := map[string]bool{}
	for r != nil {
		if seen[r.ID] || len(chain) >= 8 {
			return nil, fmt.Errorf("invalid run ancestry")
		}
		seen[r.ID] = true
		chain = append(chain, r)
		if r.Parent == "" {
			break
		}
		var ok bool
		r, ok = s.Runs[r.Parent]
		if !ok {
			return nil, fmt.Errorf("missing parent")
		}
	}
	return chain, nil
}
func (e *Engine) Create(req RunRequest, parentCapability string) (Run, string, error) {
	if err := req.Budget.Validate(); err != nil {
		return Run{}, "", err
	}
	if !validateScope(req.Scope) || (req.Region != "us" && req.Region != "eu") {
		return Run{}, "", fmt.Errorf("invalid scope or region")
	}
	now := e.now()
	cap := ID()
	run := Run{ID: ID(), Agent: ID(), Session: ID(), Budget: req.Budget, Scope: append([]string{}, req.Scope...), Region: req.Region, Created: now, Expires: now + req.Budget.Seconds*1000, LastClock: now, Buckets: map[string]Bucket{}, Window: now}
	err := e.Store.Update("run.created", now, func(s *State) error {
		if req.Parent != "" {
			parent, err := authenticate(s, parentCapability, now)
			if err != nil {
				return err
			}
			if parent.ID != req.Parent {
				return fmt.Errorf("parent identity mismatch")
			}
			chain, err := ancestors(s, parent)
			if err != nil || len(chain) >= 8 {
				return fmt.Errorf("ancestry depth limit")
			}
			for _, p := range chain {
				if p.Frozen || now >= p.Expires {
					return fmt.Errorf("parent unavailable")
				}
			}
			for _, verb := range req.Scope {
				if !contains(parent.Scope, verb) {
					return fmt.Errorf("child scope escalation")
				}
			}
			if req.Region != parent.Region {
				return fmt.Errorf("child locality escalation")
			}
			run.Parent = parent.ID
			run.Agent = parent.Agent
			run.Session = parent.Session
			if run.Expires > parent.Expires {
				run.Expires = parent.Expires
			}
		}
		s.Runs[run.ID] = &run
		s.Capabilities[Digest([]byte(cap))] = run.ID
		return nil
	})
	return run, cap, err
}
func (e *Engine) Inspect(cap string) (Run, []Action, error) {
	s, err := e.Store.View()
	if err != nil {
		return Run{}, nil, err
	}
	r, err := authenticate(&s, cap, e.now())
	if err != nil {
		return Run{}, nil, err
	}
	actions := []Action{}
	for _, a := range s.Actions {
		if a.RunID == r.ID {
			actions = append(actions, *a)
		}
	}
	return *r, actions, nil
}
