package migrate

import (
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 25,
		Up:      migrateReportTables,
	})
}

// 025: 历史迁移（曾创建 report_schedules / report_history）。
// 报表功能已移除；表由后续迁移 DROP，此处保留版本号占位。
func migrateReportTables(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	return nil
}
