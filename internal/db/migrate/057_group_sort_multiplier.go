 package migrate

 import (
 	"fmt"
 	"gorm.io/gorm"
 )

 func init() {
 	RegisterAfterAutoMigration(Migration{
 		Version: 57,
 		Up:      addGroupSortMultiplierColumns,
 	})
 }

 // 057: [历史迁移，已置空] 分组排序策略与倍率上限（Seller 移植）原为
 // groups.sort_strategy、channels.is_reserve、site_user_groups 倍率列首次接入。
 // Seller 排序/倍率子系统已整体移除，相关列由迁移 059 DROP。
 // 保留版本号占位，避免已执行过该迁移的数据库重复执行。
 func addGroupSortMultiplierColumns(database *gorm.DB) error {
 	if database == nil {
 		return fmt.Errorf("db is nil")
 	}
 	return nil
 }
