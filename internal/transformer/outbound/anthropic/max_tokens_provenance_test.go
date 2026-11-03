package anthropic

import (
	"strconv"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestConvertToAnthropicRequestMaxTokensProvenance(t *testing.T) {
	cases := []struct {
		name          string
		request       *model.InternalLLMRequest
		wantMaxTokens int64
		wantSource    string
		wantAdjusted  bool
	}{
		{
			name:          "missing limit uses the existing Anthropic default",
			request:       &model.InternalLLMRequest{Model: "claude-sonnet-4-5"},
			wantMaxTokens: 8192,
			wantSource:    model.OutboundMaxTokensSourceAnthropicDefault,
		},
		{
			name: "client max_tokens wins over its sibling alias",
			request: &model.InternalLLMRequest{
				Model:               "claude-sonnet-4-5",
				MaxTokens:           int64Ptr(100),
				MaxCompletionTokens: int64Ptr(120),
			},
			wantMaxTokens: 100,
			wantSource:    model.OutboundMaxTokensSourceClientMaxTokens,
		},
		{
			name: "client max_completion_tokens is used when max_tokens is absent",
			request: &model.InternalLLMRequest{
				Model:               "claude-sonnet-4-5",
				MaxCompletionTokens: int64Ptr(120),
			},
			wantMaxTokens: 120,
			wantSource:    model.OutboundMaxTokensSourceClientMaxCompletionTokens,
		},
		{
			name: "param override source is preserved",
			request: &model.InternalLLMRequest{
				Model:     "claude-sonnet-4-5",
				MaxTokens: int64Ptr(101),
				TransformerMetadata: map[string]string{
					model.TransformerMetadataOutboundMaxTokensSource: model.OutboundMaxTokensSourceGroupMaxTokens,
				},
			},
			wantMaxTokens: 101,
			wantSource:    model.OutboundMaxTokensSourceGroupMaxTokens,
		},
		{
			name: "thinking clamp records the actual adjusted wire value",
			request: &model.InternalLLMRequest{
				Model:           "claude-sonnet-4-5",
				MaxTokens:       int64Ptr(1000),
				ReasoningBudget: int64Ptr(2048),
			},
			wantMaxTokens: 2049,
			wantSource:    model.OutboundMaxTokensSourceClientMaxTokens,
			wantAdjusted:  true,
		},
		{
			name: "valid thinking leaves the default unadjusted",
			request: &model.InternalLLMRequest{
				Model:           "claude-sonnet-4-5",
				ReasoningBudget: int64Ptr(4096),
			},
			wantMaxTokens: 8192,
			wantSource:    model.OutboundMaxTokensSourceAnthropicDefault,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := tc.request
			request.Messages = []model.Message{{Role: "user"}}

			got := convertToAnthropicRequest(request)

			if got.MaxTokens != tc.wantMaxTokens {
				t.Fatalf("outbound max_tokens = %d, want %d", got.MaxTokens, tc.wantMaxTokens)
			}
			if gotSource := request.TransformerMetadata[model.TransformerMetadataOutboundMaxTokensSource]; gotSource != tc.wantSource {
				t.Fatalf("metadata source = %q, want %q", gotSource, tc.wantSource)
			}
			gotAdjusted := request.TransformerMetadata[model.TransformerMetadataOutboundMaxTokensAdjusted] == "true"
			if gotAdjusted != tc.wantAdjusted {
				t.Fatalf("metadata adjusted = %t, want %t", gotAdjusted, tc.wantAdjusted)
			}
			wantValue := strconv.FormatInt(tc.wantMaxTokens, 10)
			if gotValue := request.TransformerMetadata[model.TransformerMetadataOutboundMaxTokensValue]; gotValue != wantValue {
				t.Fatalf("metadata value = %q, want %q", gotValue, wantValue)
			}
		})
	}
}
