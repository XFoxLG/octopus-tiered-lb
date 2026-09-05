package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 45,
		Up:      migratePlanProviderTotalUsed,
	})
}

// 045: 历史迁移（曾为 plan_providers 增加 TotalUsed 字段）。
// 额度监控功能已移除；表由后续迁移 DROP，此处保留版本号占位。
func migratePlanProviderTotalUsed(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	return nil
}
