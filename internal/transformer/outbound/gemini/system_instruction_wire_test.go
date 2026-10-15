package gemini

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer"
	"github.com/lingyuins/octopus/internal/transformer/model"
)

// v2.9.2 fix-p2（三重对拍登记，见 conformance/deviations.json 的
// baseline-anthropic-to-gemini / baseline-chat-to-gemini）：
// Gemini proto3 JSON 的 systemInstruction 用 camelCase、且不携带空 role。
// 修复前我们发射 snake_case + "role":""，与 axonhub / CLIProxyAPI 两裁判
// 都存在纯命名差异。本测试同时防止 Role 的 omitempty 误伤 contents 里
// 真实回合的 role。
func TestGeminiOutboundSystemInstructionWireShape(t *testing.T) {
	system := "You are a conformance fixture."
	user := "Reply with the single word: OK"

	req := &model.InternalLLMRequest{
		Model: "gemini-3.1-pro-preview",
		Messages: []model.Message{
			{Role: "system", Content: model.MessageContent{Content: &system}},
			{Role: "user", Content: model.MessageContent{Content: &user}},
		},
	}

	body, err := transformer.Marshal(convertLLMToGeminiRequest(req))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(body)

	if strings.Contains(text, `"system_instruction"`) {
		t.Fatalf("必须使用 camelCase systemInstruction，实际输出: %s", text)
	}
	if !strings.Contains(text, `"systemInstruction"`) {
		t.Fatalf("缺少 systemInstruction 键: %s", text)
	}

	var payload map[string]json.RawMessage
	if err := transformer.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	var systemInstruction map[string]json.RawMessage
	if err := transformer.Unmarshal(payload["systemInstruction"], &systemInstruction); err != nil {
		t.Fatalf("unmarshal systemInstruction: %v", err)
	}
	if _, hasRole := systemInstruction["role"]; hasRole {
		t.Fatalf("systemInstruction 不允许携带空 role: %s", text)
	}
	var parts []map[string]json.RawMessage
	if err := transformer.Unmarshal(systemInstruction["parts"], &parts); err != nil {
		t.Fatalf("unmarshal parts: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("systemInstruction parts 数 = %d, want 1: %s", len(parts), text)
	}

	// contents 里的真实回合仍必须带 role（Role 的 omitempty 只对空 role 生效）。
	var contents []map[string]json.RawMessage
	if err := transformer.Unmarshal(payload["contents"], &contents); err != nil {
		t.Fatalf("unmarshal contents: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("contents 数 = %d, want 1: %s", len(contents), text)
	}
	var role string
	if err := transformer.Unmarshal(contents[0]["role"], &role); err != nil {
		t.Fatalf("contents[0] 缺少 role: %s", text)
	}
	if role != "user" {
		t.Fatalf("contents[0].role = %q, want user", role)
	}
}
