package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestAddRelayLogUsageState(t *testing.T) {
	dbPath := t.TempDir() + "/relay-log-066.db"
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	// 从旧 schema 开始（不含 usage_state），验证迁移真正加列而非 no-op。
	if err := db.Exec("CREATE TABLE relay_logs (id INTEGER PRIMARY KEY, time INTEGER, client_ip TEXT)").Error; err != nil {
		t.Fatalf("create legacy relay log table: %v", err)
	}
	if err := db.Exec("INSERT INTO relay_logs (id, time, client_ip) VALUES (1, 100, '172.17.0.1')").Error; err != nil {
		t.Fatalf("create legacy relay log: %v", err)
	}

	if err := addRelayLogUsageState(db); err != nil {
		t.Fatalf("addRelayLogUsageState: %v", err)
	}
	// 幂等：重复执行不报错。
	if err := addRelayLogUsageState(db); err != nil {
		t.Fatalf("addRelayLogUsageState idempotent: %v", err)
	}

	if !db.Migrator().HasColumn(&model.RelayLog{}, "usage_state") {
		t.Fatal("relay_logs missing column usage_state")
	}

	// 既有数据存活；历史行 usage_state 为空（无标注，前端按现状兜底）。
	var stored struct {
		ID         int64
		ClientIP   string
		UsageState string
	}
	if err := db.Table("relay_logs").Select("id", "client_ip", "usage_state").First(&stored, 1).Error; err != nil {
		t.Fatalf("query relay log: %v", err)
	}
	if stored.ClientIP != "172.17.0.1" || stored.UsageState != "" {
		t.Fatalf("unexpected legacy row values: %+v", stored)
	}
}
