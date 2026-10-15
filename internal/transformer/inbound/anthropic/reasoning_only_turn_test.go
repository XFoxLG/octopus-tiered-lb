package anthropic

import (
	"context"
	"testing"
)

// v2.9.2 fix-p2 B（scenario-reasoning-only-turn）：仅含 thinking 块、无可见
// 文本/工具调用的 assistant 回合此前被整条跳过，导致回合交替断裂成
// user→user。现保留为「空可见内容 + reasoning」，与主裁判 axonhub 及
// higress 口径一致（CLIProxyAPI 丢弃，登记为裁判分歧）。
func TestMessagesInboundKeepsReasoningOnlyAssistantTurn(t *testing.T) {
	body := []byte(`{
	  "model": "claude-sonnet-4-5",
	  "max_tokens": 64,
	  "system": "You are a conformance fixture.",
	  "messages": [
	    {"role": "user", "content": "What is the capital of France?"},
	    {"role": "assistant", "content": [
	      {"type": "thinking", "thinking": "The user asks for the capital of France.", "signature": "conformance-fixture-signature"}
	    ]},
	    {"role": "user", "content": "Answer now."}
	  ]
	}`)

	adapter := &MessagesInbound{}
	req, err := adapter.TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest error = %v", err)
	}

	if len(req.Messages) != 4 {
		t.Fatalf("messages 数 = %d, want 4（system + user + reasoning-only assistant + user）", len(req.Messages))
	}
	assistant := req.Messages[2]
	if assistant.Role != "assistant" {
		t.Fatalf("messages[2].Role = %q, want assistant（回合交替不能断裂）", assistant.Role)
	}
	if assistant.Content.Content != nil || len(assistant.Content.MultipleContent) != 0 {
		t.Fatalf("reasoning-only 回合不应有可见内容：%+v", assistant.Content)
	}
	if assistant.GetReasoningContent() != "The user asks for the capital of France." {
		t.Fatalf("reasoning 内容丢失：%q", assistant.GetReasoningContent())
	}
}

// 对照组：既无可见内容也无 reasoning 的回合仍必须跳过（不能产生空回合）。
func TestMessagesInboundStillSkipsContentlessTurnWithoutReasoning(t *testing.T) {
	body := []byte(`{
	  "model": "claude-sonnet-4-5",
	  "max_tokens": 64,
	  "messages": [
	    {"role": "user", "content": "hi"},
	    {"role": "assistant", "content": [{"type": "redacted_thinking", "data": "opaque"}]},
	    {"role": "user", "content": "continue"}
	  ]
	}`)

	adapter := &MessagesInbound{}
	req, err := adapter.TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest error = %v", err)
	}

	if len(req.Messages) != 2 {
		t.Fatalf("messages 数 = %d, want 2（redacted_thinking 无 reasoning 文本，维持跳过）", len(req.Messages))
	}
}
