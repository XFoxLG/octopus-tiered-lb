package thinking

import "testing"

// TestEffortToBudget pins the cross-protocol table. Values follow the
// CLIProxyAPI convention (minimal 512 / low 1024 / medium 8192 / high 24576 /
// xhigh 32768 / max 128000); changing them here changes every outbound
// transformer that resolves reasoning effort.
func TestEffortToBudget(t *testing.T) {
	cases := []struct {
		effort string
		want   int64
		ok     bool
	}{
		{"none", 0, true},
		{"off", 0, true}, // alias
		{"minimal", 512, true},
		{"low", 1024, true},
		{"medium", 8192, true},
		{"high", 24576, true},
		{"xhigh", 32768, true},
		{"max", 128000, true},
		{"HIGH", 24576, true},
		{"  max  ", 128000, true},
		{"", 0, false},
		{"bogus", 0, false},
	}
	for _, c := range cases {
		got, ok := EffortToBudget(c.effort)
		if ok != c.ok || got != c.want {
			t.Errorf("EffortToBudget(%q) = (%d, %v), want (%d, %v)", c.effort, got, ok, c.want, c.ok)
		}
	}
}

func TestBudgetToEffort(t *testing.T) {
	cases := []struct {
		budget int64
		want   string
		ok     bool
	}{
		{0, "", false},
		{-1, "", false},
		{1, "minimal", true},
		{512, "minimal", true},
		{513, "low", true},
		{1024, "low", true},
		{1025, "medium", true},
		{8192, "medium", true},
		{8193, "high", true},
		{24576, "high", true},
		{24577, "xhigh", true},
		{128000, "xhigh", true},
	}
	for _, c := range cases {
		got, ok := BudgetToEffort(c.budget)
		if ok != c.ok || got != c.want {
			t.Errorf("BudgetToEffort(%d) = (%q, %v), want (%q, %v)", c.budget, got, ok, c.want, c.ok)
		}
	}
}

// TestEffortBudgetRoundTrip guarantees the two directions agree for every
// tier that has a canonical budget. none/minimal are excluded by design:
// minimal maps to a budget that reads back as minimal, but none has no
// magnitude at all.
func TestEffortBudgetRoundTrip(t *testing.T) {
	for _, effort := range []string{"minimal", "low", "medium", "high", "xhigh"} {
		budget, ok := EffortToBudget(effort)
		if !ok {
			t.Fatalf("EffortToBudget(%q) not ok", effort)
		}
		back, ok := BudgetToEffort(budget)
		if !ok || back != effort {
			t.Errorf("round trip %q -> %d -> (%q, %v)", effort, budget, back, ok)
		}
	}
	// max is deliberately asymmetric: the canonical 128000 budget reads back
	// as xhigh because no budget exceeds max by definition.
	budget, ok := EffortToBudget("max")
	if !ok {
		t.Fatal("EffortToBudget(max) not ok")
	}
	if back, ok := BudgetToEffort(budget); !ok || back != "xhigh" {
		t.Errorf("max -> %d -> (%q, %v), want xhigh", budget, back, ok)
	}
}
