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
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// latestAfterMigrationVersion 是最新的 after-migration 版本号。新增迁移时同步
// 上调即可；它同时用来证明 after 链确实执行过（而不是空跑通过）。
const latestAfterMigrationVersion = 79

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
}
