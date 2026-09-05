package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 43,
		Up:      migratePoolAccountLifecycleFields,
	})
}

// 043: 历史迁移（曾为 pool_accounts 增加生命周期与调度字段）。
// 号池功能已移除；表由后续迁移 DROP，此处保留版本号占位。
func migratePoolAccountLifecycleFields(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	return nil
}
