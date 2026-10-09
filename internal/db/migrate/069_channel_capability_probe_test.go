package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

// 旧的 group_items.SupportsTools* 必须被搬到渠道×模型能力表，
// 且同一个渠道×模型出现在多个分组、结论冲突时取最新的一条。
func TestMigrateChannelCapabilityProbeFromLegacyGroupItems(t *testing.T) {
	dbPath := t.TempDir() + "/channel-069.db"
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// 旧 schema：只有 group_items 的工具探测列，没有新的能力表。
	if err := db.Exec(`CREATE TABLE group_items (
		id INTEGER PRIMARY KEY,
		group_id INTEGER NOT NULL,
		channel_id INTEGER NOT NULL,
		model_name TEXT NOT NULL,
		supports_tools INTEGER,
		supports_tools_probe_key_id INTEGER,
		supports_tools_probed_at INTEGER
	)`).Error; err != nil {
		t.Fatalf("create legacy group_items table: %v", err)
	}
	legacyRows := []struct {
		id        int
		groupID   int
		channelID int
		modelName string
		supports  any
		keyID     any
		probedAt  any
	}{
		// 同一渠道×模型出现在两个分组，结论冲突：后探测的（probedAt 更大）应胜出。
		{1, 10, 1, "gpt-4o", 1, 100, 1000},
		{2, 20, 1, "gpt-4o", 0, 100, 2000},
		// 另一个模型的明确结论应当被搬运。
		{3, 10, 1, "claude-x", 1, 101, 1500},
		// 未探测过的条目（NULL）不应产生任何记录。
		{4, 10, 2, "never-probed", nil, nil, nil},
		// 另一个渠道的同名模型是独立记录。
		{5, 30, 3, "gpt-4o", 0, 102, 1200},
	}
	for _, row := range legacyRows {
		if err := db.Exec(
			`INSERT INTO group_items
			 (id, group_id, channel_id, model_name, supports_tools, supports_tools_probe_key_id, supports_tools_probed_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			row.id, row.groupID, row.channelID, row.modelName, row.supports, row.keyID, row.probedAt,
		).Error; err != nil {
			t.Fatalf("insert legacy group item %d: %v", row.id, err)
		}
	}

	// AutoMigrate 建新表（迁移本身在检测不到表时会跳过数据平移）。
	if err := db.AutoMigrate(&model.ChannelModelCapability{}); err != nil {
		t.Fatalf("auto migrate capability table: %v", err)
	}

	if err := migrateChannelCapabilityProbe(db); err != nil {
		t.Fatalf("migrateChannelCapabilityProbe: %v", err)
	}
	// 幂等：重复执行不报错、不产生重复行。
	if err := migrateChannelCapabilityProbe(db); err != nil {
		t.Fatalf("migrateChannelCapabilityProbe idempotent: %v", err)
	}

	type stored struct {
		ChannelID  int
		ModelName  string
		Capability string
		Supported  bool
		Source     string
	}
	var storedRows []stored
	if err := db.Table("channel_model_capabilities").
		Select("channel_id", "model_name", "capability", "supported", "source").
		Order("channel_id ASC, model_name ASC").
		Scan(&storedRows).Error; err != nil {
		t.Fatalf("query migrated capabilities: %v", err)
	}

	if len(storedRows) != 3 {
		t.Fatalf("migrated %d capabilities, want 3 (NULL rows must be skipped): %+v", len(storedRows), storedRows)
	}

	byKey := make(map[string]stored, len(storedRows))
	for _, row := range storedRows {
		byKey[row.ModelName+"/"+itoaForTest(row.ChannelID)] = row
	}

	// 冲突结论取最新：channel 1 的 gpt-4o 应当是 false（probedAt=2000 的那条）。
	conflicting := byKey["gpt-4o/1"]
	if conflicting.Supported {
		t.Fatalf("conflicting capability kept the older verdict: %+v", conflicting)
	}
	if conflicting.Capability != string(model.CapabilityToolCalling) {
		t.Fatalf("capability name = %q, want %q", conflicting.Capability, model.CapabilityToolCalling)
	}
	if conflicting.Source != "migrated" {
		t.Fatalf("source = %q, want migrated (so users can tell it apart from a real probe)", conflicting.Source)
	}

	if other := byKey["claude-x/1"]; !other.Supported {
		t.Fatalf("non-conflicting capability was not migrated: %+v", other)
	}
	// 不同渠道的同名模型必须是独立记录，不能被合并。
	if otherChannel := byKey["gpt-4o/3"]; otherChannel.Supported {
		t.Fatalf("capability leaked across channels: %+v", otherChannel)
	}
}

// 新结构上已有的结论不能被迁移覆盖（用户重新探测过的结果优先）。
func TestMigrateChannelCapabilityProbeDoesNotOverwriteExisting(t *testing.T) {
	dbPath := t.TempDir() + "/channel-069-preserve.db"
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.Exec(`CREATE TABLE group_items (
		id INTEGER PRIMARY KEY,
		group_id INTEGER NOT NULL,
		channel_id INTEGER NOT NULL,
		model_name TEXT NOT NULL,
		supports_tools INTEGER,
		supports_tools_probe_key_id INTEGER,
		supports_tools_probed_at INTEGER
	)`).Error; err != nil {
		t.Fatalf("create legacy group_items table: %v", err)
	}
	if err := db.Exec(`INSERT INTO group_items
		(id, group_id, channel_id, model_name, supports_tools, supports_tools_probe_key_id, supports_tools_probed_at)
		VALUES (1, 10, 1, 'gpt-4o', 0, 100, 1000)`).Error; err != nil {
		t.Fatalf("insert legacy group item: %v", err)
	}
	if err := db.AutoMigrate(&model.ChannelModelCapability{}); err != nil {
		t.Fatalf("auto migrate capability table: %v", err)
	}
	// 预置一条"新探测得来"的结论：探测说支持，旧列说不支持。
	if err := db.Create(&model.ChannelModelCapability{
		ChannelID: 1, ModelName: "gpt-4o", Capability: model.CapabilityToolCalling,
		Supported: true, Source: "probe",
	}).Error; err != nil {
		t.Fatalf("seed existing capability: %v", err)
	}

	if err := migrateChannelCapabilityProbe(db); err != nil {
		t.Fatalf("migrateChannelCapabilityProbe: %v", err)
	}

	var count int64
	if err := db.Model(&model.ChannelModelCapability{}).
		Where("channel_id = 1 AND model_name = 'gpt-4o' AND capability = ?", model.CapabilityToolCalling).
		Count(&count).Error; err != nil {
		t.Fatalf("count capabilities: %v", err)
	}
	if count != 1 {
		t.Fatalf("capability rows = %d, want exactly 1 (migration must not duplicate)", count)
	}
	var stored model.ChannelModelCapability
	if err := db.Where("channel_id = 1 AND model_name = 'gpt-4o'").First(&stored).Error; err != nil {
		t.Fatalf("load capability: %v", err)
	}
	if !stored.Supported || stored.Source != "probe" {
		t.Fatalf("migration overwrote the newer probe verdict: %+v", stored)
	}
}

// itoaForTest 避免为测试引入 strconv 依赖歧义。
func itoaForTest(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
