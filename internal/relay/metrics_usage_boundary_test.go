package relay

import (
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"testing"
	"time"
)

func TestUsageAccountingDoesNotMutateUpstreamEvidence(t *testing.T) {
	for _, test := range []struct {
		name                   string
		prompt, output, cached int64
		anthropic              bool
	}{
		{"negative", -100, -50, -20, false},
		{"excess_cached", 10, 20, 1000, false},
		{"anthropic_separate_cache", 10, 20, 1000, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			usage := &transmodel.Usage{PromptTokens: test.prompt, CompletionTokens: test.output, AnthropicUsage: test.anthropic, CacheCreationInputTokens: -5, PromptTokensDetails: &transmodel.PromptTokensDetails{CachedTokens: test.cached}}
			metrics := NewRelayMetrics(1, "gpt-4o", "chat", "chat", "", "", nil)
			metrics.SetInternalResponse(&transmodel.InternalLLMResponse{Usage: usage}, "gpt-4o")
			if metrics.Stats.InputToken < 0 || metrics.Stats.OutputToken < 0 || metrics.Stats.InputCost < 0 || metrics.Stats.OutputCost < 0 {
				t.Fatalf("invalid accounting: %+v", metrics.Stats)
			}
			if usage.PromptTokens != test.prompt || usage.CompletionTokens != test.output || usage.PromptTokensDetails.CachedTokens != test.cached || usage.CacheCreationInputTokens != -5 {
				t.Fatal("raw upstream usage was changed")
			}
		})
	}
	usage := &transmodel.Usage{PromptTokens: 10}
	metrics := NewRelayMetrics(1, "gpt-4o", "chat", "chat", "", "", nil)
	metrics.SetInternalResponse(&transmodel.InternalLLMResponse{Usage: usage}, "gpt-4o")
	if usage.PromptTokensDetails != nil {
		t.Fatal("invented upstream cache details")
	}
}

func TestFirstOutputMilestonesAreIndependentAndStable(t *testing.T) {
	metrics := NewRelayMetrics(1, "fixture", "chat", "chat", "", "", nil)
	upstream := metrics.StartTime.Add(10 * time.Millisecond)
	client := upstream.Add(20 * time.Millisecond)
	metrics.SetUpstreamFirstVisibleTime(upstream)
	if !metrics.FirstTokenTime.IsZero() {
		t.Fatal("upstream arrival marked client delivery")
	}
	metrics.SetClientFirstFlushTime(client)
	metrics.SetUpstreamFirstVisibleTime(client.Add(time.Second))
	metrics.SetClientFirstFlushTime(client.Add(time.Second))
	if !metrics.UpstreamFirstVisibleTime.Equal(upstream) || !metrics.FirstTokenTime.Equal(client) {
		t.Fatal("first milestones were overwritten")
	}
}
