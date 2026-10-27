// Package pgchain 只承载一个回归测试：在全新的 PostgreSQL schema 上跑完整的
// 迁移链（BeforeAutoMigrate → AutoMigrate → AfterAutoMigrate）。
//
// 为什么必须单独一个包：internal/db/migrate 的迁移注册表在进程内是一次性的，
// AfterAutoMigrate 执行完会把注册列表清空，之后同一测试二进制里再调用
// db.InitDB 的测试拿到的是空列表（迁移全部空跑、只做 AutoMigrate）。
// v2.8.6 的迁移 075 用 MySQL/SQLite 的反引号写 SQL，在 PostgreSQL 上必然语法
// 报错，但 internal/op/group 里 SQLite 测试先跑掉了整个 after 链，CI 的
// postgres-stability 任务实际「零 after-migration」通过，事故因此漏到线上
// （Render 部署 v2.8.6 时进程启动即退出）。
// 独立测试包 = 独立测试二进制 = 保证这是本进程第一次、也是唯一一次数据库初始化。
package pgchain

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/backup"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// latestAfterMigrationVersion 是最新的 after-migration 版本号。新增迁移时同步
// 上调即可；它同时用来证明 after 链确实执行过（而不是空跑通过）。
const latestAfterMigrationVersion = 82

func TestFullMigrationChainOnFreshPostgres(t *testing.T) {
	dsn := os.Getenv("OCTOPUS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL test database not configured")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" {
		t.Fatal("test PostgreSQL must be local, never a production endpoint")
	}

	connection, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := connection.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	schema := fmt.Sprintf("pgchain_%d", time.Now().UnixNano())
	if err := connection.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer connection.Exec("DROP SCHEMA " + schema + " CASCADE")

	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()

	// 本进程内第一次数据库初始化：任何 after-migration 在 PostgreSQL 下失败
	// 都会在这里暴露（这正是 v2.8.6 漏掉的那条防线）。
	if err := db.InitDB("postgres", parsed.String(), false); err != nil {
		t.Fatalf("full migration chain on a fresh PostgreSQL schema failed: %v", err)
	}
	defer db.Close()

	// 第二道防线：确认 after 链真的执行过，而不是注册表被清空后空跑通过。
	var record struct {
		Status int
	}
	if err := db.GetDB().Table("migration_records").
		Where("version = ?", latestAfterMigrationVersion).
		Take(&record).Error; err != nil {
		t.Fatalf("after-migration %d was never executed on PostgreSQL: %v", latestAfterMigrationVersion, err)
	}
	if record.Status != 1 {
		t.Fatalf("after-migration %d status = %d, want success(1)", latestAfterMigrationVersion, record.Status)
	}
	for table, columns := range map[string][]string{
		"groups":                {"request_timeout_seconds", "stream_keepalive_enabled", "assistant_prefill_compat", "forward_mode"},
		"api_keys":              {"request_timeout_seconds"},
		"relay_logs":            {"diagnostics"},
		"channel_probe_results": {"protocol", "request_stream", "request_budget_ms", "request_fingerprint"},
	} {
		for _, column := range columns {
			var count int64
			if err := db.GetDB().Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?", schema, table, column).Scan(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("missing %s.%s after full chain", table, column)
			}
		}
	}

	// A PostgreSQL startup finishes migrations with DISCARD ALL. pgx also has a
	// client-side statement cache, so InitDB must reopen the pool before serving
	// traffic. Importing immediately in this same process exercises that boundary
	// and the LLMInfo string-primary-key upsert (the exact combination that once
	// failed with "prepared statement ... does not exist" followed by
	// "model value required").
	llmRows := make([]model.LLMInfo, 300)
	for i := range llmRows {
		llmRows[i] = model.LLMInfo{Name: fmt.Sprintf("pgchain-upsert-%03d", i)}
	}
	if _, err := backup.ImportWithModeToDB(
		context.Background(),
		db.GetDB(),
		&model.DBDump{Version: 5, LLMInfos: llmRows},
		model.ImportModeFull,
	); err != nil {
		t.Fatalf("first post-migration full import failed: %v", err)
	}
	var llmCount int64
	if err := db.GetDB().Model(&model.LLMInfo{}).Count(&llmCount).Error; err != nil {
		t.Fatalf("count imported LLM prices: %v", err)
	}
	if llmCount != int64(len(llmRows)) {
		t.Fatalf("LLM price count = %d, want %d", llmCount, len(llmRows))
	}
}
