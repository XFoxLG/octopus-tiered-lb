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

	geminiReq, err := convertLLMToGeminiRequest(req)
	if err != nil {
		t.Fatalf("convertLLMToGeminiRequest: %v", err)
	}
	body, err := transformer.Marshal(geminiReq)
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

func TestGeminiOutboundPreservesSystemDeveloperTextArrayOrder(t *testing.T) {
	first := "first"
	second := " second "
	developer := "developer rule"
	req := &model.InternalLLMRequest{
		Model: "gemini-3.1-pro-preview",
		Messages: []model.Message{
			{
				Role: "system",
				Content: model.MessageContent{
					MultipleContent: []model.MessageContentPart{
						{Type: "text", Text: &first},
						{Type: "text", Text: &second},
					},
				},
			},
			{Role: "user", Content: model.MessageContent{Content: thoughtStrPtr("user")}},
			{Role: "developer", Content: model.MessageContent{Content: &developer}},
		},
	}

	got, err := convertLLMToGeminiRequest(req)
	if err != nil {
		t.Fatalf("convertLLMToGeminiRequest: %v", err)
	}
	if got.SystemInstruction == nil {
		t.Fatal("systemInstruction = nil, want text parts")
	}
	if got.SystemInstruction.Role != "" {
		t.Fatalf("systemInstruction role = %q, want omitted", got.SystemInstruction.Role)
	}
	if len(got.SystemInstruction.Parts) != 3 {
		t.Fatalf("systemInstruction parts = %d, want 3", len(got.SystemInstruction.Parts))
	}
	want := []string{first, second, developer}
	for i, part := range got.SystemInstruction.Parts {
		if part.Text != want[i] {
			t.Fatalf("systemInstruction parts[%d].Text = %q, want %q", i, part.Text, want[i])
		}
	}
	if len(got.Contents) != 1 || got.Contents[0].Role != "user" {
		t.Fatalf("contents = %+v, want one user turn", got.Contents)
	}
}

func TestGeminiOutboundRejectsUnsupportedSystemContent(t *testing.T) {
	req := &model.InternalLLMRequest{
		Model: "gemini-3.1-pro-preview",
		Messages: []model.Message{
			{
				Role: "system",
				Content: model.MessageContent{
					MultipleContent: []model.MessageContentPart{
						{Type: "image_url", ImageURL: &model.ImageURL{URL: "https://example.com/image.png"}},
					},
				},
			},
		},
	}

	if _, err := convertLLMToGeminiRequest(req); err == nil {
		t.Fatal("unsupported system image silently converted, want error")
	}
}
