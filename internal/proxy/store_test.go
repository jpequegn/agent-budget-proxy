package proxy

import (
	"path/filepath"
	"testing"
	"time"
)

func setup(t *testing.T) (*Engine, string) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	e := NewEngine(s)
	e.Clock = func() time.Time { return time.Unix(1000, 0) }
	_, cap, err := e.Create(RunRequest{Budget: DefaultBudget(), Scope: []string{"inspect", "model", "paid_tool", "provision", "delete"}, Region: "us"}, "")
	if err != nil {
		t.Fatal(err)
	}
	return e, cap
}
func TestIdentity(t *testing.T) {
	e, cap := setup(t)
	r, _, err := e.Inspect(cap)
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := e.Create(RunRequest{Parent: r.ID, Budget: DefaultBudget(), Scope: []string{"inspect"}, Region: "us"}, cap)
	if err != nil || child.Agent != r.Agent || child.ID == r.ID {
		t.Fatal(child, err)
	}
	if _, _, err = e.Create(RunRequest{Parent: r.ID, Budget: DefaultBudget(), Scope: []string{"inspect"}, Region: "eu"}, cap); err == nil {
		t.Fatal("locality escalation")
	}
	if _, _, err = e.Inspect("forged"); err == nil {
		t.Fatal("forged capability")
	}
	e.Clock = func() time.Time { return time.Unix(2000, 0) }
	if _, _, err = e.Inspect(cap); err == nil {
		t.Fatal("expired")
	}
}
func TestReopenAndCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(s)
	_, cap, err := e.Create(RunRequest{Budget: DefaultBudget(), Scope: []string{"inspect"}, Region: "us"}, "")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e = NewEngine(s)
	if _, _, err = e.Inspect(cap); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE audit SET hash='tampered'"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if next, err := Open(path); err == nil {
		next.Close()
		t.Fatal("tampering accepted")
	}
}
func TestAtomicLedgerFailure(t *testing.T) {
	e, _ := setup(t)
	if _, err := e.Store.db.Exec("DROP TABLE audit"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Create(RunRequest{Budget: DefaultBudget(), Scope: []string{"inspect"}, Region: "us"}, ""); err == nil {
		t.Fatal("ledger fail open")
	}
}
