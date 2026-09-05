package migrate

import (
	"fmt"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 31,
		Up:      migrateSiteAccountSkipModelSync,
	})
}

// 031: [历史迁移，已置空] 原为站点账号增加「跳过模型同步」开关（issue #130）。
// site 子系统已整体移除，site_accounts 表由迁移 059 DROP。
// 保留版本号占位，避免已执行过该迁移的数据库重复执行。
func migrateSiteAccountSkipModelSync(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	return nil
}
