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
	if err := db.AutoMigrate(&model.RelayLog{}); err != nil {
		t.Fatalf("auto migrate relay log: %v", err)
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

	// 既有行不回填：新列保持空串，写入与读取不受影响
	logEntry := model.RelayLog{ID: 1, Time: 100, ClientIP: "172.17.0.1"}
	if err := db.Create(&logEntry).Error; err != nil {
		t.Fatalf("create relay log: %v", err)
	}
	var stored model.RelayLogListItem
	if err := db.Select("id", "client_ip", "reported_client_ip", "reported_client_ip_source").
		First(&stored, 1).Error; err != nil {
		t.Fatalf("query relay log: %v", err)
	}
	if stored.ClientIP != "172.17.0.1" || stored.ReportedClientIP != "" || stored.ReportedClientIPSource != "" {
		t.Fatalf("unexpected legacy row values: %+v", stored)
	}
}
