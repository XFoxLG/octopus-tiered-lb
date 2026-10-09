package migrate

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

// 070: supported_models 从 varchar(512) 放宽为 text。
//
// SQLite 对 varchar 长度不强制，因此这里锁两件事：
//  1. 迁移对存量表幂等、不报错、不动数据（MySQL/PG 才会真正 ALTER）；
//  2. 模型定义本身必须是 type:text —— 这是 AutoMigrate 新建库时的列类型，
//     也是 MySQL/PG 上拒写超长模型串（123 个模型 ≈ 2346 字符 → update 500）的
//     根治点，防止未来回退成 varchar(512)。
func TestMigrateChannelKeySupportedModelsToText(t *testing.T) {
	dbPath := t.TempDir() + "/channel-070.db"
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// 旧 schema：varchar(512) 的列 + 一行超长存量数据。
	if err := db.Exec(`CREATE TABLE channel_keys (
		id INTEGER PRIMARY KEY,
		channel_id INTEGER,
		enabled INTEGER,
		channel_key TEXT,
		supported_models varchar(512)
	)`).Error; err != nil {
		t.Fatalf("create legacy channel_keys table: %v", err)
	}
	long := strings.Repeat("gpt-4o-mini,", 200) + "gpt-4o" // 2408 字符，远超 512
	if err := db.Exec(
		"INSERT INTO channel_keys (id, channel_id, enabled, channel_key, supported_models) VALUES (1, 1, 1, 'sk-legacy', ?)",
		long,
	).Error; err != nil {
		t.Fatalf("insert legacy key: %v", err)
	}

	if err := migrateChannelKeySupportedModelsToText(db); err != nil {
		t.Fatalf("migrateChannelKeySupportedModelsToText: %v", err)
	}
	// 幂等：重复执行不报错。
	if err := migrateChannelKeySupportedModelsToText(db); err != nil {
		t.Fatalf("migrateChannelKeySupportedModelsToText idempotent: %v", err)
	}

	var stored string
	if err := db.Raw("SELECT supported_models FROM channel_keys WHERE id = 1").Scan(&stored).Error; err != nil {
		t.Fatalf("read legacy row: %v", err)
	}
	if stored != long {
		t.Fatalf("legacy value mutated: got %d chars, want %d", len(stored), len(long))
	}

	// 新建库（AutoMigrate 路径）的列类型必须是 text：SQLite 里声明就是 TEXT。
	freshPath := t.TempDir() + "/channel-070-fresh.db"
	fresh, err := gorm.Open(sqlite.Open(freshPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open fresh sqlite: %v", err)
	}
	freshSQL, err := fresh.DB()
	if err != nil {
		t.Fatalf("get fresh sql db: %v", err)
	}
	t.Cleanup(func() { _ = freshSQL.Close() })
	if err := fresh.AutoMigrate(&model.ChannelKey{}); err != nil {
		t.Fatalf("auto migrate channel_keys: %v", err)
	}
	colTypes, err := fresh.Migrator().ColumnTypes(&model.ChannelKey{})
	if err != nil {
		t.Fatalf("read column types: %v", err)
	}
	var dbType string
	for _, ct := range colTypes {
		if ct.Name() == "supported_models" {
			dbType = ct.DatabaseTypeName()
			break
		}
	}
	if !strings.EqualFold(dbType, "TEXT") {
		t.Fatalf("channel_keys.supported_models column type = %q, want TEXT (regressed to varchar?)", dbType)
	}
}
