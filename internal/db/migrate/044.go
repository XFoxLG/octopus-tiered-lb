package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 44,
		Up:      migratePlanProviderRefreshFields,
	})
}

// 044: 历史迁移（曾为 plan_providers 增加自动刷新与快照字段）。
// 额度监控功能已移除；表由后续迁移 DROP，此处保留版本号占位。
func migratePlanProviderRefreshFields(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	return nil
}
