package proxy

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type Credentials struct{ Admin, Reviewer, ReviewerID string }

func equalSecret(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(Digest([]byte(a))), []byte(Digest([]byte(b)))) == 1
}
func HTTP(e *Engine, tool Tool, credentials Credentials) (http.Handler, error) {
	if len(credentials.Admin) < 24 || len(credentials.Reviewer) < 24 || credentials.Admin == credentials.Reviewer || !SafeName(credentials.ReviewerID) {
		return nil, fmt.Errorf("distinct operator/reviewer secrets of at least 24 characters required")
	}
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	failure := func(w http.ResponseWriter, status int, code string) {
		respond(w, status, map[string]string{"code": code})
	}
	token := func(r *http.Request) string { return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") }
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		r.Body = http.MaxBytesReader(w, r.Body, 16384)
		if err := Decode(r.Body, v); err != nil {
			failure(w, 400, "invalid_json")
			return false
		}
		return true
	}
	agent := func(w http.ResponseWriter, r *http.Request) bool {
		if _, _, err := e.Inspect(token(r)); err != nil {
			failure(w, 403, "run_unavailable")
			return false
		}
		return true
	}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if _, err := e.Store.View(); err != nil {
			failure(w, 503, "ledger_unavailable")
			return
		}
		respond(w, 200, map[string]string{"status": "ok"})
	})
	create := func(w http.ResponseWriter, r *http.Request, child bool) {
		var req RunRequest
		if !decode(w, r, &req) {
			return
		}
		cap := ""
		if child {
			if !agent(w, r) {
				return
			}
			cap = token(r)
			if req.Parent == "" {
				failure(w, 400, "parent_required")
				return
			}
		} else {
			if !equalSecret(token(r), credentials.Admin) {
				failure(w, 401, "operator_required")
				return
			}
			if req.Parent != "" {
				failure(w, 400, "use_children_endpoint")
				return
			}
		}
		run, secret, err := e.Create(req, cap)
		if err != nil {
			failure(w, 422, "run_rejected")
			return
		}
		respond(w, 201, struct {
			Run        Run    `json:"run"`
			Capability string `json:"capability"`
		}{run, secret})
	}
	mux.HandleFunc("POST /runs", func(w http.ResponseWriter, r *http.Request) { create(w, r, false) })
	mux.HandleFunc("POST /children", func(w http.ResponseWriter, r *http.Request) { create(w, r, true) })
	mux.HandleFunc("GET /run", func(w http.ResponseWriter, r *http.Request) {
		if !agent(w, r) {
			return
		}
		run, actions, err := e.Inspect(token(r))
		if err != nil {
			failure(w, 503, "ledger_unavailable")
			return
		}
		respond(w, 200, struct {
			Run     Run      `json:"run"`
			Actions []Action `json:"actions"`
		}{run, actions})
	})
	mux.HandleFunc("POST /actions", func(w http.ResponseWriter, r *http.Request) {
		if !agent(w, r) {
			return
		}
		var req Request
		if !decode(w, r, &req) {
			return
		}
		if err := req.Validate(); err != nil {
			failure(w, 400, "invalid_action")
			return
		}
		action, err := e.Execute(r.Context(), token(r), req, tool)
		if err != nil {
			respond(w, 503, struct {
				Code   string `json:"code"`
				Action Action `json:"action"`
			}{"execution_unavailable_or_uncertain", action})
			return
		}
		status := 200
		if action.State == "awaiting_approval" || action.State == "executing" {
			status = 202
		}
		if action.State == "denied" {
			status = 422
		}
		respond(w, status, action)
	})
	mux.HandleFunc("GET /approvals", func(w http.ResponseWriter, r *http.Request) {
		if !equalSecret(token(r), credentials.Reviewer) {
			failure(w, 401, "reviewer_required")
			return
		}
		state, err := e.Store.View()
		if err != nil {
			failure(w, 503, "ledger_unavailable")
			return
		}
		actions := []Action{}
		for _, a := range state.Actions {
			if a.State == "awaiting_approval" {
				actions = append(actions, *a)
			}
		}
		respond(w, 200, actions)
	})
	mux.HandleFunc("POST /approvals", func(w http.ResponseWriter, r *http.Request) {
		if !equalSecret(token(r), credentials.Reviewer) {
			failure(w, 401, "reviewer_required")
			return
		}
		var input struct {
			ID     string `json:"id"`
			Digest string `json:"digest"`
		}
		if !decode(w, r, &input) {
			return
		}
		a, err := e.Approve(input.ID, input.Digest, credentials.ReviewerID)
		if err != nil {
			failure(w, 422, "approval_rejected")
			return
		}
		respond(w, 200, a)
	})
	return mux, nil
}
