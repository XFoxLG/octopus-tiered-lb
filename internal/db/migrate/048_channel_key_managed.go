package migrate

import (
	"fmt"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 48,
		Up:      migrateChannelKeyManaged,
	})
}

// 048: [历史迁移，已置空] channel_keys.managed 列原为 site 同步投影
// 区分"自动生成 key / 手动 key"而加。site 子系统已整体移除，该列由
// 迁移 059 DROP。保留版本号占位，避免已执行过该迁移的数据库重复执行。
func migrateChannelKeyManaged(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	return nil
}
