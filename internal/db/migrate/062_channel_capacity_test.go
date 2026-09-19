package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestAddChannelCapacityLimits(t *testing.T) {
	dbPath := t.TempDir() + "/channel-062.db"
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	// 从旧 schema 开始（不含新列），验证迁移真正加列而非 no-op。
	if err := db.Exec("CREATE TABLE channels (id INTEGER PRIMARY KEY, name TEXT)").Error; err != nil {
		t.Fatalf("create legacy channels table: %v", err)
	}
	if err := db.Exec("INSERT INTO channels (id, name) VALUES (1, 'legacy')").Error; err != nil {
		t.Fatalf("insert legacy channel: %v", err)
	}

	if err := addChannelCapacityLimits(db); err != nil {
		t.Fatalf("addChannelCapacityLimits: %v", err)
	}
	// 幂等：重复执行不报错。
	if err := addChannelCapacityLimits(db); err != nil {
		t.Fatalf("addChannelCapacityLimits idempotent: %v", err)
	}

	for _, col := range []string{"max_concurrency", "rpm_limit"} {
		if !db.Migrator().HasColumn(&model.Channel{}, col) {
			t.Fatalf("channels missing column %s", col)
		}
	}

	// 既有数据存活；新列默认 0（= 不限制）。
	var stored struct {
		ID             int
		Name           string
		MaxConcurrency int
		RPMLimit       int
	}
	if err := db.Table("channels").Select("id", "name", "max_concurrency", "rpm_limit").First(&stored, 1).Error; err != nil {
		t.Fatalf("query channel: %v", err)
	}
	if stored.Name != "legacy" || stored.MaxConcurrency != 0 || stored.RPMLimit != 0 {
		t.Fatalf("unexpected legacy row values: %+v", stored)
	}
}
