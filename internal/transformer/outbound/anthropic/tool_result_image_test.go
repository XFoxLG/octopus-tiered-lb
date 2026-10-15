package anthropic

import (
	"testing"

	anthropicModel "github.com/lingyuins/octopus/internal/transformer/inbound/anthropic"
	"github.com/lingyuins/octopus/internal/transformer/model"
)

// TestConvertToAnthropicRequestKeepsToolResultImages 回归 v2.8.14：内部格式的
// tool 消息带 image_url 部件时，转回 Anthropic 协议必须保留在 tool_result
// 内容里（data URL 还原为 source.type=base64，普通 URL 保留 source.type=url），
// 此前只保留 text 部件会把图片静默丢弃。
func TestConvertToAnthropicRequestKeepsToolResultImages(t *testing.T) {
	toolCallID := "toolu_1"
	got := convertToAnthropicRequest(&model.InternalLLMRequest{
		Model: "claude-sonnet-4-5",
		Messages: []model.Message{
			{Role: "user", Content: model.MessageContent{Content: strPtr("look")}},
			{Role: "assistant", ToolCalls: []model.ToolCall{{
				ID: "toolu_1", Type: "function",
				Function: model.FunctionCall{Name: "read_file", Arguments: `{"path":"shot.png"}`},
			}}},
			{Role: "tool", ToolCallID: &toolCallID, Content: model.MessageContent{
				MultipleContent: []model.MessageContentPart{
					{Type: "image_url", ImageURL: &model.ImageURL{URL: "data:image/png;base64,aGVsbG8="}},
					{Type: "image_url", ImageURL: &model.ImageURL{URL: "https://example.com/shot.png"}},
				},
			}},
		},
	})

	var toolResult *anthropicModel.MessageContentBlock
	for _, msg := range got.Messages {
		for i := range msg.Content.MultipleContent {
			block := &msg.Content.MultipleContent[i]
			if block.Type == "tool_result" {
				toolResult = block
			}
		}
	}
	if toolResult == nil {
		t.Fatal("tool_result block missing from converted request")
	}
	if toolResult.Content == nil || len(toolResult.Content.MultipleContent) != 2 {
		t.Fatalf("tool_result content = %+v, want 2 image blocks", toolResult.Content)
	}
	first := toolResult.Content.MultipleContent[0]
	if first.Type != "image" || first.Source == nil || first.Source.Type != "base64" ||
		first.Source.Data != "aGVsbG8=" || first.Source.MediaType != "image/png" {
		t.Fatalf("base64 image block = %+v", first)
	}
	second := toolResult.Content.MultipleContent[1]
	if second.Type != "image" || second.Source == nil || second.Source.Type != "url" ||
		second.Source.URL != "https://example.com/shot.png" {
		t.Fatalf("url image block = %+v", second)
	}
}
