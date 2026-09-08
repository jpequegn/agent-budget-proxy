package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
)

type Store struct{ db *sql.DB }
type Event struct {
	Kind      string   `json:"kind"`
	At        int64    `json:"at"`
	StateHash string   `json:"state_hash"`
	Actions   []Action `json:"actions,omitempty"`
	Runs      []Run    `json:"runs,omitempty"`
}

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	file.Close()
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Set("_txlock", "immediate")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS state (id INTEGER PRIMARY KEY CHECK(id=1), body TEXT NOT NULL); CREATE TABLE IF NOT EXISTS audit (seq INTEGER PRIMARY KEY, previous TEXT NOT NULL, hash TEXT NOT NULL, body TEXT NOT NULL)")
	if err == nil {
		b, _ := json.Marshal(emptyState())
		_, err = db.Exec("INSERT OR IGNORE INTO state VALUES (1,?)", string(b))
	}
	if err == nil {
		err = s.Verify()
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func load(tx *sql.Tx) (State, string, int64, error) {
	var state State
	var body string
	if err := tx.QueryRow("SELECT body FROM state WHERE id=1").Scan(&body); err != nil {
		return state, "", 0, err
	}
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		return state, "", 0, err
	}
	if state.Version != 1 || state.Runs == nil || state.Actions == nil || state.Capabilities == nil {
		return state, "", 0, fmt.Errorf("invalid state")
	}
	var prev, eventBody string
	var seq int64
	err := tx.QueryRow("SELECT seq,hash,body FROM audit ORDER BY seq DESC LIMIT 1").Scan(&seq, &prev, &eventBody)
	if err == sql.ErrNoRows {
		if Hash(state) != Hash(emptyState()) {
			return state, "", 0, fmt.Errorf("unaudited state")
		}
		return state, "", 0, nil
	}
	if err != nil {
		return state, "", 0, err
	}
	var event Event
	if err = json.Unmarshal([]byte(eventBody), &event); err != nil {
		return state, "", 0, err
	}
	if event.StateHash != Hash(state) {
		return state, "", 0, fmt.Errorf("state integrity mismatch")
	}
	return state, prev, seq, nil
}
func (s *Store) Update(kind string, at int64, fn func(*State) error) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, prev, seq, err := load(tx)
	if err != nil {
		return err
	}
	if seq >= 5000 {
		return fmt.Errorf("ledger event capacity reached")
	}
	before := map[string]string{}
	beforeRuns := map[string]string{}
	for k, r := range state.Runs {
		beforeRuns[k] = Hash(r)
	}
	for k, a := range state.Actions {
		before[k] = Hash(a)
	}
	if err = fn(&state); err != nil {
		return err
	}
	if len(state.Runs) > 100 || len(state.Actions) > 1000 {
		return fmt.Errorf("ledger state capacity reached")
	}
	event := Event{Kind: kind, At: at, StateHash: Hash(state)}
	for k, r := range state.Runs {
		if beforeRuns[k] != Hash(r) {
			event.Runs = append(event.Runs, *r)
		}
	}
	for k, a := range state.Actions {
		if before[k] != Hash(a) {
			event.Actions = append(event.Actions, *a)
		}
	}
	body, _ := json.Marshal(state)
	if len(body) > 4*1024*1024 {
		return fmt.Errorf("state too large")
	}
	audit, _ := json.Marshal(event)
	sum := Digest([]byte(prev + "\n" + string(audit)))
	if _, err = tx.Exec("UPDATE state SET body=? WHERE id=1", string(body)); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO audit VALUES (?,?,?,?)", seq+1, prev, sum, string(audit)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) View() (State, error) {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return State{}, err
	}
	defer tx.Rollback()
	state, _, _, err := load(tx)
	return state, err
}
func (s *Store) Verify() error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT seq,previous,hash,body FROM audit ORDER BY seq")
	if err != nil {
		return err
	}
	prev := ""
	var expected int64 = 1
	for rows.Next() {
		var seq int64
		var previous, sum, body string
		if err = rows.Scan(&seq, &previous, &sum, &body); err != nil {
			rows.Close()
			return err
		}
		if seq != expected || previous != prev || sum != Digest([]byte(prev+"\n"+body)) {
			rows.Close()
			return fmt.Errorf("audit integrity mismatch")
		}
		expected++
		prev = sum
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	_, _, _, err = load(tx)
	return err
}
func (s *Store) Events() ([]Event, error) {
	if err := s.Verify(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query("SELECT body FROM audit ORDER BY seq")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var b string
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var e Event
		if err = json.Unmarshal([]byte(b), &e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
