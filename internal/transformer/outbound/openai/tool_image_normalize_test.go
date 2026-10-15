package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

// TestNormalizeMessageForOpenAICompatKeepsToolImages 回归 v2.8.14：tool 消息
// 内的 image_url 部件不能被 OpenAI-compat 归一化扁平化丢弃（此前会退化成
// 空字符串 content，含图工具结果在出站前消失）。
func TestNormalizeMessageForOpenAICompatKeepsToolImages(t *testing.T) {
	toolCallID := "toolu_1"
	msg := model.Message{
		Role:       "tool",
		ToolCallID: &toolCallID,
		Content: model.MessageContent{
			MultipleContent: []model.MessageContentPart{
				{Type: "image_url", ImageURL: &model.ImageURL{URL: "https://example.com/shot.png"}},
			},
		},
	}

	normalizeMessageForOpenAICompat(&msg)

	if msg.Content.Content != nil {
		t.Fatalf("tool image content must stay an array, got string %q", *msg.Content.Content)
	}
	if len(msg.Content.MultipleContent) != 1 || msg.Content.MultipleContent[0].Type != "image_url" {
		t.Fatalf("tool image parts lost: %+v", msg.Content)
	}

	raw, err := json.Marshal(msg.Content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	if !strings.Contains(string(raw), "image_url") || !strings.Contains(string(raw), "shot.png") {
		t.Fatalf("serialized tool content lost image: %s", raw)
	}
}

// TestNormalizeMessageForOpenAICompatKeepsMixedToolImages 覆盖混合 text+image
// （两种部件都保留）与纯文本 tool 消息（维持字符串扁平化的旧行为）。
func TestNormalizeMessageForOpenAICompatKeepsMixedToolImages(t *testing.T) {
	text := "done"
	mixed := model.Message{
		Role: "tool",
		Content: model.MessageContent{
			MultipleContent: []model.MessageContentPart{
				{Type: "text", Text: &text},
				{Type: "image_url", ImageURL: &model.ImageURL{URL: "data:image/png;base64,AAAA"}},
			},
		},
	}
	normalizeMessageForOpenAICompat(&mixed)
	if len(mixed.Content.MultipleContent) != 2 {
		t.Fatalf("mixed tool content parts = %d, want 2", len(mixed.Content.MultipleContent))
	}

	plain := model.Message{
		Role: "tool",
		Content: model.MessageContent{
			MultipleContent: []model.MessageContentPart{
				{Type: "text", Text: &text},
			},
		},
	}
	normalizeMessageForOpenAICompat(&plain)
	if plain.Content.Content == nil || *plain.Content.Content != "done" {
		t.Fatalf("plain text tool content must be flattened to string, got %+v", plain.Content)
	}
}
