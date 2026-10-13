package migrate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newContentCapSettingsDB 建一张仅含 settings(key,value) 的最小库。
func newContentCapSettingsDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "content-cap.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`).Error; err != nil {
		t.Fatalf("create settings table: %v", err)
	}
	return db
}

func contentCapSettingValues(t *testing.T, db *gorm.DB) map[string]string {
	t.Helper()
	rows := make([]struct {
		Key   string
		Value string
	}, 0)
	if err := db.Table("settings").Order("key").Find(&rows).Error; err != nil {
		t.Fatalf("read settings: %v", err)
	}
	values := make(map[string]string, len(rows))
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	return values
}

// 只删目标行，其它设置项原样保留。
func TestRemoveRelayLogContentCapSetting_DeletesOnlyTargetRow(t *testing.T) {
	db := newContentCapSettingsDB(t)
	seed := [][2]string{
		{"relay_log_max_content_size_mb", "2"},
		{"relay_log_memory_log_max_dimidiate_times", "15"},
		{"relay_log_queue_drop_policy", "oldest"},
	}
	for _, row := range seed {
		if err := db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, row[0], row[1]).Error; err != nil {
			t.Fatalf("insert %s: %v", row[0], err)
		}
	}

	if err := removeRelayLogContentCapSetting(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	values := contentCapSettingValues(t, db)
	if _, ok := values["relay_log_max_content_size_mb"]; ok {
		t.Fatalf("target setting row was not deleted: %+v", values)
	}
	if values["relay_log_memory_log_max_dimidiate_times"] != "15" || values["relay_log_queue_drop_policy"] != "oldest" {
		t.Fatalf("unrelated settings changed: %+v", values)
	}
}

// 重复执行（迁移记录丢失后重跑）不报错、也不影响结果。
func TestRemoveRelayLogContentCapSetting_Idempotent(t *testing.T) {
	db := newContentCapSettingsDB(t)
	if err := db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, "relay_log_max_content_size_mb", "2").Error; err != nil {
		t.Fatalf("insert: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := removeRelayLogContentCapSetting(db); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if values := contentCapSettingValues(t, db); len(values) != 0 {
		t.Fatalf("unexpected rows left: %+v", values)
	}
}

// settings 表还不存在（极早的库）时静默跳过，不返回错误。
func TestRemoveRelayLogContentCapSetting_MissingTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "empty.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := removeRelayLogContentCapSetting(db); err != nil {
		t.Fatalf("missing settings table should be a no-op: %v", err)
	}
}

// 方言回归：迁移 075 曾经直接写反引号 `key`，PostgreSQL 会把 backtick 解析成
// 自定义操作符，迁移报 syntax error at or near "=" (42601)，v2.8.6 因此在
// Render 启动即失败。此断言在 PostgreSQL 方言的 DryRun 会话上检查真实生成的
// SQL：不能出现反引号，必须是双引号列名。
func TestRemoveRelayLogContentCapSetting_PostgresStatementHasNoBackticks(t *testing.T) {
	// sql.Open 是惰性的：DriverName=pgx + DryRun 不会真的连库，无需 PostgreSQL。
	database, err := gorm.Open(postgres.Open("postgres://octopus:octopus@127.0.0.1:1/octopus_test?sslmode=disable"), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true, // 没有 PostgreSQL 也不连库，只借用方言的 quote 行为
		SkipDefaultTransaction: true, // 否则 Delete 会先 Begin 事务，DryRun 也照样拨号
		Logger:                 logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open postgres dialector in dry-run mode: %v", err)
	}

	tx := relayLogContentCapSettingDelete(database.Session(&gorm.Session{DryRun: true}))
	if tx.Error != nil {
		t.Fatalf("build delete statement: %v", tx.Error)
	}
	sql := tx.Statement.SQL.String()
	if strings.ContainsRune(sql, '`') {
		t.Fatalf("PostgreSQL 语句中仍含反引号，方言不安全: %s", sql)
	}
	if !strings.Contains(sql, `DELETE FROM "settings"`) || !strings.Contains(sql, `"key"`) {
		t.Fatalf("unexpected PostgreSQL delete statement: %s", sql)
	}
}
