package gemini

import (
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func thoughtStrPtr(s string) *string { return &s }

// fix-p0：多轮 reasoning 上下文。assistant 的 reasoning 必须还原为
// thought part（thought:true），否则含 reasoning 的回合会退化。
// 对拍基准：axonhub / CLIProxyAPI 均保留该 thought part。
func TestConvertAssistantReasoningBecomesThoughtPart(t *testing.T) {
	reasoning := "Internal reasoning for the first answer."
	answer := "First answer."

	req := &model.InternalLLMRequest{
		Model: "gemini-3.1-pro-preview",
		Messages: []model.Message{
			{Role: "user", Content: model.MessageContent{Content: thoughtStrPtr("First question.")}},
			{Role: "assistant", Content: model.MessageContent{Content: &answer}, ReasoningContent: &reasoning},
			{Role: "user", Content: model.MessageContent{Content: thoughtStrPtr("Second question.")}},
		},
	}

	got := convertLLMToGeminiRequest(req)
	if len(got.Contents) != 3 {
		t.Fatalf("contents 数 = %d, want 3", len(got.Contents))
	}
	turn := got.Contents[1]
	if turn.Role != "model" {
		t.Fatalf("contents[1].role = %q, want model", turn.Role)
	}
	if len(turn.Parts) != 2 {
		t.Fatalf("contents[1].parts 数 = %d, want 2（thought + text）", len(turn.Parts))
	}
	if !turn.Parts[0].Thought || turn.Parts[0].Text != reasoning {
		t.Fatalf("parts[0] = %+v, want thought part（text=%q）", turn.Parts[0], reasoning)
	}
	if turn.Parts[1].Thought || turn.Parts[1].Text != answer {
		t.Fatalf("parts[1] = %+v, want 可见正文 part（text=%q）", turn.Parts[1], answer)
	}
}

// Responses 尾随 reasoning item（仅有 reasoning、无可见正文）至少产出
// 一个非空 thought part；不得再出现空 parts 的 model turn（上游 400 来源）。
func TestConvertTrailingReasoningOnlyProducesNonEmptyModelTurn(t *testing.T) {
	reasoning := "Considered the previous answer."

	req := &model.InternalLLMRequest{
		Model: "gemini-3.1-pro-preview",
		Messages: []model.Message{
			{Role: "user", Content: model.MessageContent{Content: thoughtStrPtr("Continue the analysis.")}},
			{Role: "assistant", ReasoningContent: &reasoning},
		},
	}

	got := convertLLMToGeminiRequest(req)
	if len(got.Contents) != 2 {
		t.Fatalf("contents 数 = %d, want 2", len(got.Contents))
	}
	turn := got.Contents[1]
	if turn.Role != "model" {
		t.Fatalf("contents[1].role = %q, want model", turn.Role)
	}
	if len(turn.Parts) != 1 {
		t.Fatalf("contents[1].parts 数 = %d, want 1", len(turn.Parts))
	}
	if !turn.Parts[0].Thought || turn.Parts[0].Text != reasoning {
		t.Fatalf("parts[0] = %+v, want thought part（text=%q）", turn.Parts[0], reasoning)
	}
}

// Reasoning 是 ReasoningContent 的等价字段（部分提供商使用）。
func TestConvertAssistantReasoningAltFieldBecomesThoughtPart(t *testing.T) {
	reasoning := "alt-field reasoning"

	req := &model.InternalLLMRequest{
		Model: "gemini-3.1-pro-preview",
		Messages: []model.Message{
			{Role: "user", Content: model.MessageContent{Content: thoughtStrPtr("hi")}},
			{Role: "assistant", Reasoning: &reasoning},
		},
	}

	got := convertLLMToGeminiRequest(req)
	if len(got.Contents) != 2 || len(got.Contents[1].Parts) != 1 {
		t.Fatalf("contents = %+v, want 1 个 thought part 的 model turn", got.Contents)
	}
	if !got.Contents[1].Parts[0].Thought || got.Contents[1].Parts[0].Text != reasoning {
		t.Fatalf("parts[0] = %+v, want thought part", got.Contents[1].Parts[0])
	}
}

// ReasoningSignature 是 Anthropic 签名载体（跨协议签名对 Gemini 无效），
// 明确不转发，避免把外协议签名塞给 Gemini 被拒。
func TestConvertReasoningSignatureNotForwardedToGemini(t *testing.T) {
	reasoning := "reasoning with foreign signature"
	signature := "anthropic-thinking-signature"

	req := &model.InternalLLMRequest{
		Model: "gemini-3.1-pro-preview",
		Messages: []model.Message{
			{Role: "user", Content: model.MessageContent{Content: thoughtStrPtr("hi")}},
			{Role: "assistant", ReasoningContent: &reasoning, ReasoningSignature: &signature},
		},
	}

	got := convertLLMToGeminiRequest(req)
	if len(got.Contents) != 2 || len(got.Contents[1].Parts) != 1 {
		t.Fatalf("contents = %+v, want 1 个 thought part 的 model turn", got.Contents)
	}
	if sig := got.Contents[1].Parts[0].ThoughtSignature; sig != "" {
		t.Fatalf("thoughtSignature = %q, want 空（不转发外协议签名）", sig)
	}
}

// 完全空的 assistant 回合（无正文、无 reasoning、无工具调用）整体丢弃，
// 不留空 parts 的 model turn。
func TestConvertEmptyAssistantTurnDropped(t *testing.T) {
	req := &model.InternalLLMRequest{
		Model: "gemini-3.1-pro-preview",
		Messages: []model.Message{
			{Role: "user", Content: model.MessageContent{Content: thoughtStrPtr("hi")}},
			{Role: "assistant"},
		},
	}

	got := convertLLMToGeminiRequest(req)
	if len(got.Contents) != 1 {
		t.Fatalf("contents 数 = %d, want 1（空 model 回合被丢弃）", len(got.Contents))
	}
}

// 工具调用回合的 sentinel 行为保持原样（Gemini 3 函数调用签名豁免）。
func TestConvertAssistantToolCallsKeepSentinelUnchanged(t *testing.T) {
	req := &model.InternalLLMRequest{
		Model: "gemini-3.1-pro-preview",
		Messages: []model.Message{
			{Role: "user", Content: model.MessageContent{Content: thoughtStrPtr("hi")}},
			{Role: "assistant", ToolCalls: []model.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: model.FunctionCall{
						Name:      "lookup",
						Arguments: `{"q":"x"}`,
					},
				},
			}},
		},
	}

	got := convertLLMToGeminiRequest(req)
	if len(got.Contents) != 2 {
		t.Fatalf("contents 数 = %d, want 2", len(got.Contents))
	}
	parts := got.Contents[1].Parts
	if len(parts) != 1 || parts[0].FunctionCall == nil {
		t.Fatalf("parts = %+v, want 单个 functionCall part", parts)
	}
	if parts[0].ThoughtSignature != "skip_thought_signature_validator" {
		t.Fatalf("thoughtSignature = %q, want sentinel 保持", parts[0].ThoughtSignature)
	}
}
