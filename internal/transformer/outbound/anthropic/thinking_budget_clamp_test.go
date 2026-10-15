package anthropic

import (
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func int64Ptr(v int64) *int64 { return &v }

// 对齐 new-api relaykit 的三条既有用例（claude_default_max_tokens_test.go）
// 加一条「本来就合法、不该动」的用例。
func TestClampThinkingBudget_MatchesNewAPISemantics(t *testing.T) {
	cases := []struct {
		name         string
		budget       int64
		maxTokens    int64
		wantBudget   int64
		wantMax      int64
		wantAdjusted bool
	}{
		{
			name: "budget below 1024 is raised",
			// new-api: budget 512 + max 4096 → budget 1024, max 不变
			budget: 512, maxTokens: 4096,
			wantBudget: 1024, wantMax: 4096, wantAdjusted: true,
		},
		{
			name: "budget at max_tokens is lowered",
			// new-api: budget 4096 + max 4096 → budget 4095, max 不变
			budget: 4096, maxTokens: 4096,
			wantBudget: 4095, wantMax: 4096, wantAdjusted: true,
		},
		{
			name: "max_tokens too small for manual thinking is raised",
			// new-api: budget 2048 + max 1000 → max 抬高到 budget+1
			budget: 2048, maxTokens: 1000,
			wantBudget: 2048, wantMax: 2049, wantAdjusted: true,
		},
		{
			name:   "valid pair is untouched",
			budget: 2048, maxTokens: 4096,
			wantBudget: 2048, wantMax: 4096, wantAdjusted: false,
		},
		{
			name: "tiny max_tokens without budget hint uses the 1280 floor",
			// budget 极小（例如 1）时不该把 max_tokens 抬到 2，而是用保底 1280。
			budget: 1, maxTokens: 512,
			wantBudget: 1024, wantMax: 1280, wantAdjusted: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotBudget, gotMax, gotAdjusted := clampThinkingBudget(tc.budget, tc.maxTokens)
			if gotBudget != tc.wantBudget || gotMax != tc.wantMax || gotAdjusted != tc.wantAdjusted {
				t.Fatalf(
					"clampThinkingBudget(%d, %d) = (%d, %d, %v), want (%d, %d, %v)",
					tc.budget, tc.maxTokens, gotBudget, gotMax, gotAdjusted,
					tc.wantBudget, tc.wantMax, tc.wantAdjusted,
				)
			}
		})
	}
}

// 出站集成：客户端 budget_tokens 与 max_tokens 相等时，出站请求必须已经合法
// （budget = max_tokens - 1），不再把 400 透传给上游。
func TestConvertToAnthropicRequest_ClampsBudgetAtMaxTokens(t *testing.T) {
	got := convertToAnthropicRequest(&model.InternalLLMRequest{
		Model:           "claude-sonnet-4-5",
		MaxTokens:       int64Ptr(4096),
		ReasoningBudget: int64Ptr(4096),
		Messages: []model.Message{{
			Role:    "user",
			Content: model.MessageContent{Content: strPtr("hi")},
		}},
	})

	if got.MaxTokens != 4096 {
		t.Fatalf("max_tokens = %d, want 4096", got.MaxTokens)
	}
	if got.Thinking == nil || got.Thinking.BudgetTokens == nil {
		t.Fatalf("thinking/budget 缺失: %#v", got.Thinking)
	}
	if *got.Thinking.BudgetTokens != 4095 {
		t.Fatalf("budget_tokens = %d, want 4095", *got.Thinking.BudgetTokens)
	}
}

// 出站集成：max_tokens 装不下最小预算时抬高 max_tokens，budget 原样保留。
func TestConvertToAnthropicRequest_RaisesMaxTokensForTinyLimit(t *testing.T) {
	got := convertToAnthropicRequest(&model.InternalLLMRequest{
		Model:           "claude-sonnet-4-5",
		MaxTokens:       int64Ptr(1000),
		ReasoningBudget: int64Ptr(2048),
		Messages: []model.Message{{
			Role:    "user",
			Content: model.MessageContent{Content: strPtr("hi")},
		}},
	})

	if got.MaxTokens != 2049 {
		t.Fatalf("max_tokens = %d, want 2049", got.MaxTokens)
	}
	if got.Thinking == nil || got.Thinking.BudgetTokens == nil || *got.Thinking.BudgetTokens != 2048 {
		t.Fatalf("budget_tokens = %#v, want 2048", got.Thinking)
	}
}

// 默认 max_tokens（客户端没给上限 → 8192）大于 budget 时不得无谓调整。
func TestConvertToAnthropicRequest_DefaultMaxTokensKeepsValidBudget(t *testing.T) {
	got := convertToAnthropicRequest(&model.InternalLLMRequest{
		Model:           "claude-sonnet-4-5",
		ReasoningBudget: int64Ptr(4096),
		Messages: []model.Message{{
			Role:    "user",
			Content: model.MessageContent{Content: strPtr("hi")},
		}},
	})

	if got.MaxTokens != 8192 {
		t.Fatalf("max_tokens = %d, want 8192（默认值不应被调整）", got.MaxTokens)
	}
	if got.Thinking == nil || got.Thinking.BudgetTokens == nil || *got.Thinking.BudgetTokens != 4096 {
		t.Fatalf("budget_tokens = %#v, want 4096（4096 < 8192，合法）", got.Thinking)
	}
}
