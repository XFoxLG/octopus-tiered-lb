package relay

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	transformerModel "github.com/lingyuins/octopus/internal/transformer/model"
)

// TestCountRequestImages 覆盖三种承载形态与「空 payload 不计数」口径：
// content 数组 / tool 消息内的图片 / legacy Message.Images。
func TestCountRequestImages(t *testing.T) {
	req := &transformerModel.InternalLLMRequest{
		Model: "gpt-4o",
		Messages: []transformerModel.Message{
			{Role: "user", Content: transformerModel.MessageContent{MultipleContent: []transformerModel.MessageContentPart{
				{Type: "text", Text: strPtr("look")},
				{Type: "image_url", ImageURL: &transformerModel.ImageURL{URL: "data:image/png;base64,AAAA"}},
			}}},
			{Role: "user", Content: transformerModel.MessageContent{MultipleContent: []transformerModel.MessageContentPart{
				{Type: "image_url"}, // 无 payload，不计数
				{Type: "image_url", ImageURL: &transformerModel.ImageURL{}}, // 空 URL，不计数
			}}},
			{Role: "tool", ToolCallID: strPtr("t1"), Content: transformerModel.MessageContent{MultipleContent: []transformerModel.MessageContentPart{
				{Type: "image_url", ImageURL: &transformerModel.ImageURL{URL: "https://example.com/a.png"}},
			}}},
			{Role: "user", Images: []transformerModel.MessageContentPart{
				{Type: "image_url", ImageURL: &transformerModel.ImageURL{URL: "https://example.com/legacy.png"}},
			}},
		},
	}
	if got := countRequestImages(req); got != 3 {
		t.Fatalf("countRequestImages = %d, want 3", got)
	}
	if got := countRequestImages(nil); got != 0 {
		t.Fatalf("nil request must yield 0, got %d", got)
	}
}

// TestSaveLogWritesImageCount 端到端验证 saveLog 接线：请求内的图片数量写入
// relayLog.ImageCount（日志「含图 ×N」标注与 has_image 筛选用），与内容留存
// 开关无关（这里未开启正文留存）。
func TestSaveLogWritesImageCount(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "metrics-image-count.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatalf("InitLogDB(shared) failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	t.Cleanup(relaylog.SetCacheForTest(nil))

	metrics := NewRelayMetrics(1, "gemini-3.1-pro", "chat", "chat", "", "", &transformerModel.InternalLLMRequest{
		Model: "gemini-3.1-pro",
		Messages: []transformerModel.Message{
			{Role: "user", Content: transformerModel.MessageContent{MultipleContent: []transformerModel.MessageContentPart{
				{Type: "image_url", ImageURL: &transformerModel.ImageURL{URL: "data:image/png;base64,AAAA"}},
				{Type: "image_url", ImageURL: &transformerModel.ImageURL{URL: "https://example.com/b.png"}},
			}}},
		},
	})
	metrics.SetInternalResponse(estimateFixtureResponse(), "gemini-3.1-pro")
	metrics.saveLog(context.Background(), nil, 9*time.Second, []model.ChannelAttempt{
		{ChannelID: 1, ChannelName: "ch", ModelName: "gemini-3.1-pro", Status: model.AttemptSuccess},
	}, 1, "ch")

	logs, _ := relaylog.GetCacheAndLock()
	if len(logs) == 0 {
		t.Fatal("expected a log row in cache")
	}
	last := logs[len(logs)-1]
	if last.ImageCount != 2 {
		t.Fatalf("ImageCount = %d, want 2", last.ImageCount)
	}
}
