package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

// v2.9.2 fix-p2 B：reasoning-only 回合在 anthropic 出站必须只回吐 thinking
// 块——此前会落到空内容分支序列化成 `"content":{}`，strict Anthropic 上游
// 会直接 400。
func TestConvertToAnthropicRequestReasoningOnlyTurnEmitsThinkingBlock(t *testing.T) {
	reasoning := "The user asks for the capital of France."
	signature := "conformance-fixture-signature"

	req := &model.InternalLLMRequest{
		Model: "claude-sonnet-4-5",
		Messages: []model.Message{
			{Role: "user", Content: model.MessageContent{Content: strPtr("What is the capital of France?")}},
			{Role: "assistant", ReasoningContent: &reasoning, ReasoningSignature: &signature},
			{Role: "user", Content: model.MessageContent{Content: strPtr("Answer now.")}},
		},
	}

	raw, err := json.Marshal(convertToAnthropicRequest(req))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(raw)

	if strings.Contains(text, `"content":{}`) {
		t.Fatalf("reasoning-only 回合产出空 content：%s", text)
	}
	if !strings.Contains(text, `"type":"thinking"`) {
		t.Fatalf("thinking 块缺失：%s", text)
	}
	if !strings.Contains(text, `"thinking":"The user asks for the capital of France."`) {
		t.Fatalf("thinking 文本丢失：%s", text)
	}
	if !strings.Contains(text, `"signature":"conformance-fixture-signature"`) {
		t.Fatalf("thinking 签名丢失：%s", text)
	}
	if strings.Contains(text, `"type":"text"`) {
		t.Fatalf("reasoning-only 回合不应产出空 text 块：%s", text)
	}
}
