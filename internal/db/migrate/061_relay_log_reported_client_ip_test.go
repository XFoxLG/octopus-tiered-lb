package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func TestAddRelayLogReportedClientIP(t *testing.T) {
	dbPath := t.TempDir() + "/relay-log-061.db"
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	// Start from an actual legacy schema, not the current model which already
	// contains both new columns and would make this migration a no-op.
	if err := db.Exec("CREATE TABLE relay_logs (id INTEGER PRIMARY KEY, time INTEGER, client_ip TEXT)").Error; err != nil {
		t.Fatalf("create legacy relay log table: %v", err)
	}
	if err := db.Exec("INSERT INTO relay_logs (id, time, client_ip) VALUES (1, 100, ?)", "172.17.0.1").Error; err != nil {
		t.Fatalf("create legacy relay log: %v", err)
	}

	if err := addRelayLogReportedClientIP(db); err != nil {
		t.Fatalf("addRelayLogReportedClientIP: %v", err)
	}
	// 幂等：重复执行不报错
	if err := addRelayLogReportedClientIP(db); err != nil {
		t.Fatalf("addRelayLogReportedClientIP idempotent: %v", err)
	}

	for _, col := range []string{"reported_client_ip", "reported_client_ip_source"} {
		if !db.Migrator().HasColumn(&model.RelayLog{}, col) {
			t.Fatalf("relay_logs missing column %s", col)
		}
	}

	// Existing data survives; nullable new columns read as empty strings.
	var stored model.RelayLogListItem
	if err := db.Select("id", "client_ip", "reported_client_ip", "reported_client_ip_source").
		First(&stored, 1).Error; err != nil {
		t.Fatalf("query relay log: %v", err)
	}
	if stored.ClientIP != "172.17.0.1" || stored.ReportedClientIP != "" || stored.ReportedClientIPSource != "" {
		t.Fatalf("unexpected legacy row values: %+v", stored)
	}
}
