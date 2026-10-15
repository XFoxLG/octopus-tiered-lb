package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

// v2.8.15 协议层 fix-p1（scenario-system-developer-role）：
// responses 入站的 developer 消息在 anthropic 出站曾被整体丢弃；
// axonhub / CLIProxyAPI 均把 developer 并入 system（保持输入顺序）。
func TestConvertToAnthropicRequestMergesDeveloperIntoSystem(t *testing.T) {
	req := &model.InternalLLMRequest{
		Model: "claude-sonnet-4-5",
		Messages: []model.Message{
			{Role: "system", Content: model.MessageContent{Content: strPtr("System level instruction.")}},
			{Role: "developer", Content: model.MessageContent{Content: strPtr("Developer level instruction.")}},
			{Role: "user", Content: model.MessageContent{Content: strPtr("Say hi.")}},
		},
	}

	got := convertToAnthropicRequest(req)
	if got.System == nil || len(got.System.MultiplePrompts) != 2 {
		t.Fatalf("system = %#v, want 两块（system + developer）", got.System)
	}
	if got.System.MultiplePrompts[0].Text != "System level instruction." ||
		got.System.MultiplePrompts[1].Text != "Developer level instruction." {
		t.Fatalf("system 顺序 = %#v, want 按输入顺序 system→developer", got.System.MultiplePrompts)
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != "user" {
		t.Fatalf("messages = %#v, want 仅一条 user（developer 不进 messages）", got.Messages)
	}
}

// v2.8.15 协议层 fix-p1（scenario-thinking-signature-wire）：
// thinking 块必须始终带 signature 键（无真实签名时空串）；
// 缺键会让严格客户端（如 Grok Build）报 missing field signature。
func TestThinkingBlockAlwaysCarriesSignatureKey(t *testing.T) {
	reasoning := "Deliberation before answering."

	build := func(sig *string) *model.InternalLLMRequest {
		return &model.InternalLLMRequest{
			Model: "claude-sonnet-4-5",
			Messages: []model.Message{
				{Role: "user", Content: model.MessageContent{Content: strPtr("Think first.")}},
				{
					Role:               "assistant",
					Content:            model.MessageContent{Content: strPtr("The answer.")},
					ReasoningContent:   &reasoning,
					ReasoningSignature: sig,
				},
				{Role: "user", Content: model.MessageContent{Content: strPtr("Continue.")}},
			},
		}
	}

	// 无签名 → 空串键必须存在（纯文本回合路径）
	rawNoSig, err := json.Marshal(convertToAnthropicRequest(build(nil)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(rawNoSig), `"signature":""`) {
		t.Fatalf("thinking 块缺 signature 键：%s", string(rawNoSig))
	}

	// 有真实签名 → 原样保留
	sig := "real-signature"
	rawSig, err := json.Marshal(convertToAnthropicRequest(build(&sig)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(rawSig), `"signature":"real-signature"`) {
		t.Fatalf("真实签名未原样保留：%s", string(rawSig))
	}

	// 带工具调用的回合（convertAssistantWithToolCalls 路径）同样保键
	withTool := build(&sig)
	withTool.Messages[1].ToolCalls = []model.ToolCall{{
		ID:   "call_1",
		Type: "function",
		Function: model.FunctionCall{
			Name:      "lookup",
			Arguments: `{"q":"x"}`,
		},
	}}
	rawTool, err := json.Marshal(convertToAnthropicRequest(withTool))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(rawTool), `"signature":"real-signature"`) {
		t.Fatalf("工具调用回合的 thinking 块签名异常：%s", string(rawTool))
	}
}
