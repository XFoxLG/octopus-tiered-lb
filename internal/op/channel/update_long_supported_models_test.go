package channel

import (
	"context"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
)

// TestUpdatePersistsLongSupportedModels 回归测试：填充「支持模型」时上游返回的
// 模型列表逗号连接后可轻松超过旧的 varchar(512) 上限（实测一个聚合站 123 个模型
// 即达 2346 字符），MySQL/PostgreSQL 严格模式下拒写 → /api/v1/channel/update 返回
// 500。supported_models 列已放宽为 text（迁移 070），此处用超长值锁住该行为，
// 防止回退。SQLite 对 varchar 长度不强制，因此本测试在 SQLite 下通过不代表
// MySQL/PG 修复有效——它锁的是"写入超长值不报错且数据完整"这一行为契约。
func TestUpdatePersistsLongSupportedModels(t *testing.T) {
	setupBatchGroupTest(t)
	seedChannel(t, 1, 1)

	key := &model.ChannelKey{ChannelID: 1, Enabled: true, ChannelKey: "sk-test-long"}
	if err := db.GetDB().Create(key).Error; err != nil {
		t.Fatalf("seed key failed: %v", err)
	}
	if err := RefreshCacheByID(1, context.Background()); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}

	// 构造一个远超 varchar(512) 的模型串（模拟真实填充结果）
	long := strings.Repeat("gpt-4o-mini,", 200) // 2400 字符
	req := &model.ChannelUpdateRequest{
		ID:           1,
		KeysToUpdate: []model.ChannelKeyUpdateRequest{{ID: key.ID, SupportedModels: &long}},
	}
	if _, err := Update(req, context.Background()); err != nil {
		t.Fatalf("Update with long supported_models failed (the 500): %v", err)
	}

	var got model.ChannelKey
	if err := db.GetDB().First(&got, key.ID).Error; err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if got.SupportedModels != long {
		t.Fatalf("supported_models truncated: got %d chars, want %d", len(got.SupportedModels), len(long))
	}
}
