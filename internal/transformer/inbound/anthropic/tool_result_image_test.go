package anthropic

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

// TestMessagesInboundToolResultImagesArePreserved 回归 v2.8.14：tool_result
// 内的图片（base64 与 URL 两种 source）都必须保留为 image_url 部件——此前
// URL 变体被静默丢弃，tool content 变成空数组。
func TestMessagesInboundToolResultImagesArePreserved(t *testing.T) {
	body := []byte(`{
	  "model": "claude-sonnet-4-5",
	  "max_tokens": 64,
	  "messages": [
	    {"role": "user", "content": "look"},
	    {"role": "assistant", "content": [
	      {"type": "tool_use", "id": "toolu_1", "name": "read_file", "input": {"path": "shot.png"}}
	    ]},
	    {"role": "user", "content": [
	      {"type": "tool_result", "tool_use_id": "toolu_1", "content": [
	        {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "aGVsbG8="}},
	        {"type": "image", "source": {"type": "url", "url": "https://example.com/shot.png"}}
	      ]}
	    ]}
	  ]
	}`)

	adapter := &MessagesInbound{}
	req, err := adapter.TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest error = %v", err)
	}

	var toolMsg *model.Message
	for i := range req.Messages {
		if req.Messages[i].Role == "tool" {
			toolMsg = &req.Messages[i]
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("tool message missing from internal request")
	}

	parts := toolMsg.Content.MultipleContent
	if len(parts) != 2 {
		t.Fatalf("tool content parts len = %d, want 2 (%+v)", len(parts), parts)
	}
	if parts[0].Type != "image_url" || parts[0].ImageURL == nil || parts[0].ImageURL.URL != "data:image/png;base64,aGVsbG8=" {
		t.Fatalf("base64 image part = %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "https://example.com/shot.png" {
		t.Fatalf("url image part = %+v", parts[1])
	}
}
