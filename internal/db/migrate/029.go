package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 29,
		Up:      migrateAlertRuleScopes,
	})
}

// 029: 历史迁移（曾为 alert_rules 增加滑动窗口与作用域字段，issue #128）。
// 告警规则引擎已移除；表由后续迁移 DROP，此处保留版本号占位。
func migrateAlertRuleScopes(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	return nil
}
