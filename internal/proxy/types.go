package proxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Budget struct {
	Cost    int64 `json:"cost_micros"`
	Tokens  int64 `json:"tokens"`
	Writes  int64 `json:"writes"`
	Seconds int64 `json:"seconds"`
	Burst   int64 `json:"burst"`
	Refill  int64 `json:"refill_per_second"`
}

func (b Budget) Validate() error {
	if b.Cost < 1 || b.Cost > 1e9 || b.Tokens < 1 || b.Tokens > 1e7 || b.Writes < 0 || b.Writes > 1000 ||
		b.Seconds < 1 || b.Seconds > 3600 || b.Burst < 1 || b.Burst > 100 || b.Refill < 1 || b.Refill > 100 {
		return fmt.Errorf("invalid budget")
	}
	return nil
}
func DefaultBudget() Budget {
	return Budget{Cost: 100000, Tokens: 10000, Writes: 5, Seconds: 600, Burst: 20, Refill: 2}
}

type RunRequest struct {
	Parent string   `json:"parent,omitempty"`
	Budget Budget   `json:"budget"`
	Scope  []string `json:"scope"`
	Region string   `json:"region"`
}
type Bucket struct {
	Milli int64 `json:"milli"`
	At    int64 `json:"at"`
}
type Run struct {
	ID             string            `json:"id"`
	Agent          string            `json:"agent"`
	Session        string            `json:"session"`
	Parent         string            `json:"parent,omitempty"`
	CapabilityHash string            `json:"-"`
	Budget         Budget            `json:"budget"`
	Scope          []string          `json:"scope"`
	Region         string            `json:"region"`
	Created        int64             `json:"created"`
	Expires        int64             `json:"expires"`
	LastClock      int64             `json:"last_clock"`
	Spent          int64             `json:"spent_micros"`
	Held           int64             `json:"held_micros"`
	Tokens         int64             `json:"tokens"`
	HeldTokens     int64             `json:"held_tokens"`
	Writes         int64             `json:"writes"`
	Frozen         bool              `json:"frozen"`
	Buckets        map[string]Bucket `json:"buckets"`
	Window         int64             `json:"window"`
	Deletes        int64             `json:"deletes"`
	FailedWrites   int64             `json:"failed_writes"`
}
type Request struct {
	Key       string `json:"key"`
	Verb      string `json:"verb"`
	Resource  string `json:"resource"`
	Units     int64  `json:"units"`
	MaxTokens int64  `json:"max_tokens"`
	DataClass string `json:"data_class"`
}

func (r Request) Validate() error {
	if len(r.Key) < 1 || len(r.Key) > 64 || len(r.Verb) < 1 || len(r.Verb) > 32 || len(r.Resource) > 64 ||
		r.Units < 1 || r.Units > 1000 || r.MaxTokens < 0 || r.MaxTokens > 1000000 || (r.DataClass != "public" && r.DataClass != "internal") {
		return fmt.Errorf("invalid action")
	}
	return nil
}

type Decision struct {
	Kind      string   `json:"kind"`
	Rule      string   `json:"rule"`
	Reason    string   `json:"reason"`
	Steering  []string `json:"steering,omitempty"`
	Remaining int64    `json:"remaining_micros"`
}
type Outcome struct {
	Cost      int64  `json:"cost_micros"`
	Tokens    int64  `json:"tokens"`
	Effects   int64  `json:"effects"`
	Success   bool   `json:"success"`
	LatencyMS int64  `json:"latency_ms"`
	Detail    string `json:"detail"`
}
type Action struct {
	ID             string   `json:"id"`
	RunID          string   `json:"run_id"`
	Agent          string   `json:"agent"`
	Session        string   `json:"session"`
	Request        Request  `json:"request"`
	Fingerprint    string   `json:"fingerprint"`
	Effective      Request  `json:"effective"`
	Decision       Decision `json:"decision"`
	State          string   `json:"state"`
	Reserved       int64    `json:"reserved_micros"`
	ReservedTokens int64    `json:"reserved_tokens"`
	At             int64    `json:"at"`
	SettledAt      int64    `json:"settled_at,omitempty"`
	ApprovedBy     string   `json:"approved_by,omitempty"`
	ApprovalUntil  int64    `json:"approval_until,omitempty"`
	Outcome        *Outcome `json:"outcome,omitempty"`
}
type State struct {
	Version      int                `json:"version"`
	Runs         map[string]*Run    `json:"runs"`
	Capabilities map[string]string  `json:"capabilities"`
	Actions      map[string]*Action `json:"actions"`
}

func emptyState() State {
	return State{Version: 1, Runs: map[string]*Run{}, Capabilities: map[string]string{}, Actions: map[string]*Action{}}
}
func ID() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func Hash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return Digest(b)
}
func Digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func Decode(r io.Reader, v any) error {
	d := json.NewDecoder(io.LimitReader(r, 16385))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON value")
	}
	return nil
}
func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
func actionKey(run, key string) string { return run + "/" + key }
func validateScope(scope []string) bool {
	if len(scope) == 0 || len(scope) > 8 {
		return false
	}
	for _, s := range scope {
		if !contains([]string{"inspect", "model", "paid_tool", "provision", "delete"}, s) {
			return false
		}
	}
	return true
}
func SafeName(s string) bool {
	return len(s) > 0 && len(s) <= 64 && !strings.ContainsAny(s, "\x00\r\n")
}
