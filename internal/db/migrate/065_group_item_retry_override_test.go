package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestAddGroupItemRetryOverride(t *testing.T) {
	dbPath := t.TempDir() + "/group-item-retry-065.db"
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	// 从旧 schema 开始（group_items 不含新列），验证迁移真正加列而非 no-op。
	if err := db.Exec("CREATE TABLE group_items (id INTEGER PRIMARY KEY, group_id INTEGER, channel_id INTEGER, model_name TEXT)").Error; err != nil {
		t.Fatalf("create legacy group_items table: %v", err)
	}
	if err := db.Exec("INSERT INTO group_items (id, group_id, channel_id, model_name) VALUES (1, 1, 1, 'legacy')").Error; err != nil {
		t.Fatalf("insert legacy group item: %v", err)
	}

	if err := addGroupItemRetryOverride(db); err != nil {
		t.Fatalf("addGroupItemRetryOverride: %v", err)
	}
	// 幂等：重复执行不报错。
	if err := addGroupItemRetryOverride(db); err != nil {
		t.Fatalf("addGroupItemRetryOverride idempotent: %v", err)
	}

	if !db.Migrator().HasColumn(&model.GroupItem{}, "relay_retry_count_override") {
		t.Fatal("group_items missing column relay_retry_count_override")
	}

	// 既有数据存活；新列 NULL = 跟随渠道/分组/全局，行为不变。
	var stored struct {
		ID                      int
		ModelName               string
		RelayRetryCountOverride *int
	}
	if err := db.Table("group_items").Select("id", "model_name", "relay_retry_count_override").First(&stored, 1).Error; err != nil {
		t.Fatalf("query group item: %v", err)
	}
	if stored.ModelName != "legacy" || stored.RelayRetryCountOverride != nil {
		t.Fatalf("unexpected legacy row values: %+v", stored)
	}
}
