package migrate

import (
	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
	"testing"
)

func TestLifecycleMigrationsExpandLegacySchemaIdempotently(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(t.TempDir()+"/lifecycle.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := database.DB()
	defer connection.Close()
	for _, sql := range []string{`CREATE TABLE groups(id INTEGER PRIMARY KEY,name TEXT)`, `CREATE TABLE api_keys(id INTEGER PRIMARY KEY,name TEXT)`, `CREATE TABLE relay_logs(id INTEGER PRIMARY KEY)`, `INSERT INTO groups VALUES(1,'existing')`, `INSERT INTO api_keys VALUES(1,'existing-key')`, `INSERT INTO relay_logs VALUES(1)`} {
		if err := database.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := migrateRelayRequestLifecycle(database); err != nil {
			t.Fatal(err)
		}
		if err := migrateGroupForwardMode(database); err != nil {
			t.Fatal(err)
		}
	}
	var group model.Group
	if err := database.First(&group).Error; err != nil {
		t.Fatal(err)
	}
	if group.Name != "existing" || group.RequestTimeoutSeconds != 0 || group.StreamKeepaliveEnabled || group.AssistantPrefillCompat || group.ForwardMode != "inherit" {
		t.Fatalf("migration changed config: %+v", group)
	}
	var key model.APIKey
	if err := database.First(&key).Error; err != nil {
		t.Fatal(err)
	}
	if key.RequestTimeoutSeconds != 0 || key.Name != "existing-key" {
		t.Fatal("migration changed key")
	}
	var log model.RelayLog
	if err := database.First(&log).Error; err != nil {
		t.Fatal(err)
	}
	if log.Diagnostics != nil {
		t.Fatal("migration invented diagnostics")
	}
	if err := database.Model(&group).Updates(map[string]any{"request_timeout_seconds": 900, "stream_keepalive_enabled": true, "assistant_prefill_compat": true, "forward_mode": "raw"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateRelayRequestLifecycle(database); err != nil {
		t.Fatal(err)
	}
	if err := migrateGroupForwardMode(database); err != nil {
		t.Fatal(err)
	}
	if err := database.First(&group).Error; err != nil {
		t.Fatal(err)
	}
	if group.RequestTimeoutSeconds != 900 || !group.StreamKeepaliveEnabled || group.ForwardMode != "raw" {
		t.Fatal("rerun reset explicit configuration")
	}
}
