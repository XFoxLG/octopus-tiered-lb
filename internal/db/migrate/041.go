package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 41,
		Up:      migratePoolAccountColumns,
	})
}

// 041: 历史迁移（曾为 pool_accounts 增加扩展列）。
// 号池功能已移除；表由后续迁移 DROP，此处保留版本号占位。
func migratePoolAccountColumns(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	return nil
}
