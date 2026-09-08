package proxy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBillingExportIncludesChargedFailuresNotHolds(t *testing.T) {
	e, cap := setup(t)
	req := Request{Key: "failed", Verb: "paid_tool", Resource: "paid-search", Units: 1, DataClass: "public"}
	if _, err := e.Execute(context.Background(), cap, req, &MockTool{Fail: true}); err != nil {
		t.Fatal(err)
	}
	req.Key = "unknown"
	if _, err := e.Execute(context.Background(), cap, req, &MockTool{Uncertain: true}); err == nil {
		t.Fatal("expected uncertainty")
	}
	events, err := e.Store.BillingEvents("local_lab")
	if err != nil || len(events) != 1 || events[0].Metadata["cost_micros"] != int64(250) || events[0].Metadata["success"] != false {
		t.Fatal(events, err)
	}
	again, err := e.Store.BillingEvents("local_lab")
	if err != nil || Hash(events) != Hash(again) {
		t.Fatal("unstable export", err)
	}
	b, _ := json.Marshal(events)
	if strings.Contains(string(b), cap) {
		t.Fatal("secret in export")
	}
	if _, err := e.Store.BillingEvents("Not Valid"); err == nil {
		t.Fatal("invalid customer")
	}
}
