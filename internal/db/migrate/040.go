package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 40,
		Up:      migrateAccountPool,
	})
}

// 040: [历史迁移，已置空] 原用于创建号池结构并把 "pool" 写入导航设置。
// 号池功能已移除，表和列由后续迁移 DROP；保留版本号占位，避免旧数据库重复执行。
func migrateAccountPool(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	return nil
}
