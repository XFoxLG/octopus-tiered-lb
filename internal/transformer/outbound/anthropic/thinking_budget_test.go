package anthropic

import (
	"testing"

	anthropicModel "github.com/lingyuins/octopus/internal/transformer/inbound/anthropic"
	crossThinking "github.com/lingyuins/octopus/internal/transformer/thinking"
)

func TestGetThinkingBudget_MapsExtendedEfforts(t *testing.T) {
	cases := map[string]int64{
		anthropicModel.EffortLow:    crossThinking.ThresholdLow,
		anthropicModel.EffortMedium: crossThinking.ThresholdMedium,
		anthropicModel.EffortHigh:   crossThinking.ThresholdHigh,
		"xhigh":                     crossThinking.BudgetXHigh,
		anthropicModel.EffortMax:    crossThinking.BudgetMax,
		// Unknown efforts keep the historical 8192 fallback: the caller only
		// reaches this branch with an explicit budget pointer, so emitting a
		// concrete number is still safer than dropping thinking entirely.
		"unknown": crossThinking.BudgetDefault,
		"MINIMAL": crossThinking.ThresholdMinimal,
	}
	for effort, want := range cases {
		got := getThinkingBudget(effort, nil)
		if got == nil {
			t.Fatalf("effort %q: budget is nil", effort)
		}
		if *got != want {
			t.Fatalf("effort %q: budget=%d, want %d", effort, *got, want)
		}
	}
}

func TestGetThinkingBudget_ExplicitBudgetWins(t *testing.T) {
	budget := int64(4242)
	got := getThinkingBudget(anthropicModel.EffortMax, &budget)
	if got == nil || *got != budget {
		t.Fatalf("expected explicit budget %d, got %#v", budget, got)
	}
}
