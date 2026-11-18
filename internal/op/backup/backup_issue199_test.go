package backup

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	internaldb "github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// TestImportDisabledChannelsRoundTrip 是 issue #199 的回归测试：
// 禁用的渠道/渠道 Key 经「导出 → JSON 往返 → 完整恢复导入」后必须保持禁用。
//
// 根因：Channel.Enabled / ChannelKey.Enabled 是非指针 bool 且带 gorm:"default:true"，
// GORM Create 会省略零值列让数据库填默认值，导入时 enabled=false 被写成 true。
// 修复后 batchInsert 用 Select(全部实际列名) 强制零值也写入 INSERT。
func TestImportDisabledChannelsRoundTrip(t *testing.T) {
	srcPath := filepath.Join(t.TempDir(), "src.db")
	if err := internaldb.InitDB("sqlite", srcPath, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })

	dbConn := internaldb.GetDB()

	var defaultGroupCount int64
	if err := dbConn.Model(&model.ChannelGroup{}).Where("is_default = ?", true).Count(&defaultGroupCount).Error; err != nil {
		t.Fatal(err)
	}
	if defaultGroupCount == 0 {
		if err := dbConn.Create(&model.ChannelGroup{
			Name: model.DefaultChannelGroupName, IsDefault: true,
		}).Error; err != nil {
			t.Fatalf("seed default channel group: %v", err)
		}
	}

	// 渠道 1：禁用，其 key 也禁用。
	// 注意：直接 Create 一个 Enabled=false 的渠道同样会因 GORM 零值省略而
	// 落成默认值 true，因此先用 Create 写入再用显式 UPDATE 置为禁用，
	// 模拟用户在 UI 中关闭渠道的真实路径（channel 更新接口显式写列）。
	disabledCh := model.Channel{
		ID: 1, Name: "disabled-channel", GroupID: 1,
		Type: outbound.OutboundTypeOpenAIChat, Enabled: true,
	}
	if err := dbConn.Create(&disabledCh).Error; err != nil {
		t.Fatalf("seed disabled channel: %v", err)
	}
	if err := dbConn.Model(&model.Channel{}).Where("id = ?", 1).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable channel: %v", err)
	}
	disabledKey := model.ChannelKey{
		ID: 1, ChannelID: 1, Enabled: true, ChannelKey: "sk-disabled",
	}
	if err := dbConn.Create(&disabledKey).Error; err != nil {
		t.Fatalf("seed disabled key: %v", err)
	}
	if err := dbConn.Model(&model.ChannelKey{}).Where("id = ?", 1).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable key: %v", err)
	}

	// 渠道 2：启用（对照组）。
	enabledCh := model.Channel{
		ID: 2, Name: "enabled-channel", GroupID: 1,
		Type: outbound.OutboundTypeOpenAIChat, Enabled: true,
	}
	if err := dbConn.Create(&enabledCh).Error; err != nil {
		t.Fatalf("seed enabled channel: %v", err)
	}

	// 导出 → JSON 往返（模拟浏览器导出/导入文件）。
	dump, err := ExportAll(context.Background(), false, false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatalf("marshal dump: %v", err)
	}
	var restored model.DBDump
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal dump: %v", err)
	}

	// 校验导出 JSON 本身未丢失 enabled=false（导出侧一直正确，防止回归）。
	for _, ch := range restored.Channels {
		if ch.ID == 1 && ch.Enabled {
			t.Fatal("export dropped disabled channel's enabled=false")
		}
	}
	for _, k := range restored.ChannelKeys {
		if k.ChannelID == 1 && k.Enabled {
			t.Fatal("export dropped disabled key's enabled=false")
		}
	}

	// 全新目标库做完整恢复导入。
	targetPath := filepath.Join(t.TempDir(), "target.db")
	target, err := internaldb.OpenStandalone("sqlite", targetPath, false)
	if err != nil {
		t.Fatalf("open target db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := target.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	if err := internaldb.Migrate(target); err != nil {
		t.Fatalf("migrate target: %v", err)
	}

	if _, err := ImportWithModeToDB(context.Background(), target, &restored, model.ImportModeFull); err != nil {
		t.Fatalf("full import: %v", err)
	}

	var gotCh1, gotCh2 model.Channel
	if err := target.First(&gotCh1, 1).Error; err != nil {
		t.Fatalf("load channel 1: %v", err)
	}
	if err := target.First(&gotCh2, 2).Error; err != nil {
		t.Fatalf("load channel 2: %v", err)
	}
	if gotCh1.Enabled {
		t.Fatal("disabled channel became enabled after import")
	}
	if !gotCh2.Enabled {
		t.Fatal("enabled channel became disabled after import")
	}

	var gotKey model.ChannelKey
	if err := target.First(&gotKey, 1).Error; err != nil {
		t.Fatalf("load key 1: %v", err)
	}
	if gotKey.Enabled {
		t.Fatal("disabled channel key became enabled after import")
	}
}
