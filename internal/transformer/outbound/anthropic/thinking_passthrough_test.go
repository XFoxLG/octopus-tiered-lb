package anthropic

import (
	"testing"

	anthropicModel "github.com/lingyuins/octopus/internal/transformer/inbound/anthropic"
	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestConvertToAnthropicRequest_PassthroughEffortAsAdaptive(t *testing.T) {
	for effort, want := range map[string]string{
		"low":     "low",
		"medium":  "medium",
		"high":    "high",
		"xhigh":   "xhigh",
		"max":     "max",
		"MINIMAL": "low", // not an Anthropic tier; collapses to the lowest one
		"minimal": "low",
	} {
		got := convertToAnthropicRequest(&model.InternalLLMRequest{
			Model:           "claude-opus-4-8",
			ReasoningEffort: effort,
			Messages: []model.Message{{
				Role:    "user",
				Content: model.MessageContent{Content: strPtr("hi")},
			}},
		})
		if got.Thinking == nil || got.Thinking.Type != anthropicModel.ThinkingTypeAdaptive {
			t.Fatalf("effort %q: expected adaptive thinking, got %#v", effort, got.Thinking)
		}
		if got.Thinking.BudgetTokens != nil {
			t.Fatalf("effort %q: expected no budget_tokens, got %v", effort, *got.Thinking.BudgetTokens)
		}
		if got.OutputConfig == nil || got.OutputConfig.Effort != want {
			t.Fatalf("effort %q: expected output_config.effort=%q, got %#v", effort, want, got.OutputConfig)
		}
	}
}

func TestConvertToAnthropicRequest_ExplicitBudgetKeepsEnabledBudget(t *testing.T) {
	budget := int64(12345)
	got := convertToAnthropicRequest(&model.InternalLLMRequest{
		Model:           "claude-sonnet-4-6",
		MaxTokens:       int64Ptr(20000), // > budget，合法组合：本用例只验证透传形状
		ReasoningEffort: "max",
		ReasoningBudget: &budget,
		Messages: []model.Message{{
			Role:    "user",
			Content: model.MessageContent{Content: strPtr("hi")},
		}},
	})
	if got.Thinking == nil || got.Thinking.Type != anthropicModel.ThinkingTypeEnabled {
		t.Fatalf("expected enabled thinking, got %#v", got.Thinking)
	}
	if got.Thinking.BudgetTokens == nil || *got.Thinking.BudgetTokens != budget {
		t.Fatalf("expected budget %d, got %#v", budget, got.Thinking.BudgetTokens)
	}
	if got.OutputConfig != nil {
		t.Fatalf("expected no output_config when explicit budget is set, got %#v", got.OutputConfig)
	}
}

func TestConvertToAnthropicRequest_NoEffortOmitsThinking(t *testing.T) {
	got := convertToAnthropicRequest(&model.InternalLLMRequest{
		Model: "claude-sonnet-4-6",
		Messages: []model.Message{{
			Role:    "user",
			Content: model.MessageContent{Content: strPtr("hi")},
		}},
	})
	if got.Thinking != nil {
		t.Fatalf("expected no thinking, got %#v", got.Thinking)
	}
	if got.OutputConfig != nil {
		t.Fatalf("expected no output_config, got %#v", got.OutputConfig)
	}
}

// TestConvertToAnthropicRequest_NonPositiveBudgetOmitsThinking pins the
// disable/dynamic semantics: Anthropic's enabled thinking cannot express
// budget 0 or -1 (the schema requires 1024 ≤ budget < max_tokens), so instead
// of clamping "disable" up to an unwanted 1024-token thinking block the field
// is omitted entirely. This is the path a `model(none)` suffix takes.
func TestConvertToAnthropicRequest_NonPositiveBudgetOmitsThinking(t *testing.T) {
	for _, budget := range []int64{0, -1} {
		got := convertToAnthropicRequest(&model.InternalLLMRequest{
			Model:           "claude-sonnet-4-6",
			ReasoningBudget: &budget,
			Messages: []model.Message{{
				Role:    "user",
				Content: model.MessageContent{Content: strPtr("hi")},
			}},
		})
		if got.Thinking != nil {
			t.Fatalf("budget=%d: expected thinking omitted, got %#v", budget, got.Thinking)
		}
		if got.OutputConfig != nil {
			t.Fatalf("budget=%d: expected no output_config, got %#v", budget, got.OutputConfig)
		}
	}
}

func strPtr(s string) *string { return &s }
