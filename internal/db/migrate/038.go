package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 38,
		Up:      migratePlanProviderFiveHour,
	})
}

// 038: 历史迁移（曾为 plan_providers 增加 5 小时窗口配额列）。
// 额度监控功能已移除；表由后续迁移 DROP，此处保留版本号占位。
func migratePlanProviderFiveHour(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	return nil
}
