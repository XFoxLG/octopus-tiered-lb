package openai

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

// v2.9.2 fix-p2 B：reasoning-only 回合在 generic chat 出站必须归一为
// `content:""`（不再产生无内容的 role-only 消息），且 generic 渠道不外发
// reasoning_content——与 axonhub/higress 的降级口径一致。
func TestChatOutboundReasoningOnlyTurnBecomesEmptyContent(t *testing.T) {
	reasoning := "Considered the previous answer."

	req := &model.InternalLLMRequest{
		Model: "claude-sonnet-4-5",
		Messages: []model.Message{
			{Role: "user", Content: model.MessageContent{Content: strPtr("Hi")}},
			{Role: "assistant", ReasoningContent: &reasoning},
			{Role: "user", Content: model.MessageContent{Content: strPtr("Continue")}},
		},
	}

	out := &ChatOutbound{}
	httpReq, err := out.TransformRequest(context.Background(), req, "https://upstream.invalid", "sk-test")
	if err != nil {
		t.Fatalf("TransformRequest error = %v", err)
	}
	raw, err := io.ReadAll(httpReq.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var body struct {
		Messages []struct {
			Role             string          `json:"role"`
			Content          json.RawMessage `json:"content"`
			ReasoningContent *string         `json:"reasoning_content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal body: %v; body=%s", err, string(raw))
	}
	if len(body.Messages) != 3 {
		t.Fatalf("messages 数 = %d, want 3：%s", len(body.Messages), string(raw))
	}
	assistant := body.Messages[1]
	if assistant.Role != "assistant" {
		t.Fatalf("messages[1].role = %q, want assistant：%s", assistant.Role, string(raw))
	}
	if string(assistant.Content) != `""` {
		t.Fatalf("reasoning-only 回合 content = %s, want \"\"：%s", string(assistant.Content), string(raw))
	}
	if assistant.ReasoningContent != nil {
		t.Fatalf("generic 渠道不应外发 reasoning_content：%q", *assistant.ReasoningContent)
	}
}
