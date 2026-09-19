package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestAddRelayRetryOverrides(t *testing.T) {
	dbPath := t.TempDir() + "/relay-retry-063.db"
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	// 从旧 schema 开始（channels/groups 都不含新列），验证迁移真正加列而非 no-op。
	if err := db.Exec("CREATE TABLE channels (id INTEGER PRIMARY KEY, name TEXT)").Error; err != nil {
		t.Fatalf("create legacy channels table: %v", err)
	}
	if err := db.Exec("CREATE TABLE groups (id INTEGER PRIMARY KEY, name TEXT)").Error; err != nil {
		t.Fatalf("create legacy groups table: %v", err)
	}
	if err := db.Exec("INSERT INTO channels (id, name) VALUES (1, 'legacy')").Error; err != nil {
		t.Fatalf("insert legacy channel: %v", err)
	}
	if err := db.Exec("INSERT INTO groups (id, name) VALUES (1, 'legacy')").Error; err != nil {
		t.Fatalf("insert legacy group: %v", err)
	}

	if err := addRelayRetryOverrides(db); err != nil {
		t.Fatalf("addRelayRetryOverrides: %v", err)
	}
	// 幂等：重复执行不报错。
	if err := addRelayRetryOverrides(db); err != nil {
		t.Fatalf("addRelayRetryOverrides idempotent: %v", err)
	}

	if !db.Migrator().HasColumn(&model.Channel{}, "relay_retry_count_override") {
		t.Fatal("channels missing column relay_retry_count_override")
	}
	for _, col := range []string{"relay_retry_count", "relay_route_retries"} {
		if !db.Migrator().HasColumn(&model.Group{}, col) {
			t.Fatalf("groups missing column %s", col)
		}
	}

	// 既有数据存活；新列默认 -1（= 跟随分组/全局，行为不变）。
	var storedChannel struct {
		ID                      int
		Name                    string
		RelayRetryCountOverride int
	}
	if err := db.Table("channels").Select("id", "name", "relay_retry_count_override").First(&storedChannel, 1).Error; err != nil {
		t.Fatalf("query channel: %v", err)
	}
	if storedChannel.Name != "legacy" || storedChannel.RelayRetryCountOverride != -1 {
		t.Fatalf("unexpected legacy channel values: %+v", storedChannel)
	}
	var storedGroup struct {
		ID                int
		Name              string
		RelayRetryCount   int
		RelayRouteRetries int
	}
	if err := db.Table("groups").Select("id", "name", "relay_retry_count", "relay_route_retries").First(&storedGroup, 1).Error; err != nil {
		t.Fatalf("query group: %v", err)
	}
	if storedGroup.Name != "legacy" || storedGroup.RelayRetryCount != -1 || storedGroup.RelayRouteRetries != -1 {
		t.Fatalf("unexpected legacy group values: %+v", storedGroup)
	}
}
