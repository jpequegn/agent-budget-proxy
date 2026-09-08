package proxy

import (
	"strings"
	"testing"
)

func TestContracts(t *testing.T) {
	if err := DefaultBudget().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"agent":"forged"}`, `{} {}`, `{"cost_micros":1.2}`} {
		var b Budget
		if Decode(strings.NewReader(body), &b) == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	if DefaultBudget().Cost != 100000 {
		t.Fatal("money unit")
	}
	if ID() == ID() {
		t.Fatal("duplicate identity")
	}
}
