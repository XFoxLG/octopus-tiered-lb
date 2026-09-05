package migrate

import (
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 26,
		Up: migrateNotificationTables,
	})
}

// 026: 历史迁移（曾创建 notifications 及其投递/偏好/策略附表）。
// 通知已收敛为应用内内核；附表由后续迁移 DROP，此处保留版本号占位。
// notifications 表本身仍由 db.go 的 AutoMigrate 维护。
func migrateNotificationTables(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	return nil
}
