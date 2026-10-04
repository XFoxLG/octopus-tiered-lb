package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 70,
		Up:      migrateChannelKeySupportedModelsToText,
	})
}

// 070: 放宽 channel_keys.supported_models 列类型为 text。
// 原列为 varchar(512)：填充「支持模型」时上游返回的模型列表逗号连接后可轻松超过
// 512 字符（实测一个聚合站 123 个模型即达 2346 字符），MySQL/PostgreSQL 严格模式
// 下直接拒写 → /api/v1/channel/update 返回 500。SQLite 对 varchar 长度不强制（按
// TEXT 存储），无需修改；模型定义已改为 type:text，AutoMigrate 不会回退存量列，
// 因此这里对 MySQL/PG 显式 ALTER。幂等：重复执行 ALTER 到 text 安全。
func migrateChannelKeySupportedModelsToText(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channel_keys") {
		return nil
	}
	if !db.Migrator().HasColumn("channel_keys", "supported_models") {
		return nil
	}

	switch db.Dialector.Name() {
	case "mysql":
		if err := db.Exec("ALTER TABLE channel_keys MODIFY COLUMN supported_models TEXT").Error; err != nil {
			return fmt.Errorf("alter channel_keys.supported_models to text (mysql): %w", err)
		}
	case "postgres", "postgresql":
		if err := db.Exec("ALTER TABLE channel_keys ALTER COLUMN supported_models TYPE TEXT").Error; err != nil {
			return fmt.Errorf("alter channel_keys.supported_models to text (postgres): %w", err)
		}
	default:
		// sqlite 及其它：varchar(512) 仅是类型亲和，不强制长度，无需修改。
	}
	return nil
}
