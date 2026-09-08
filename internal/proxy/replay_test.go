package proxy

import (
	"context"
	"io"
	"path/filepath"
	"testing"
)

func TestReplayAndComparison(t *testing.T) {
	results, err := Experiment(filepath.Join(t.TempDir(), "comparison"))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatal(results)
	}
	for _, r := range results {
		if !r.Replay.Verified || r.Reads != 3 {
			t.Fatal(r)
		}
	}
	if results[0].Effects <= results[1].Effects || results[0].Spent <= results[1].Spent {
		t.Fatal("no improvement", results)
	}
	if results[2].Spent > 100000 {
		t.Fatal("budget exceeded")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestTelemetryFailureDoesNotDisableLedger(t *testing.T) {
	e, cap := setup(t)
	provider, err := Telemetry(context.Background(), "", brokenWriter{})
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Shutdown(context.Background())
	e.Tracer = provider.Tracer("test")
	a, err := e.Execute(context.Background(), cap, request("observed"), &MockTool{})
	if err != nil || a.State != "settled" {
		t.Fatal(a, err)
	}
	if err = e.Store.Verify(); err != nil {
		t.Fatal(err)
	}
}
