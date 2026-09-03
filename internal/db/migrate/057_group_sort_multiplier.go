 package migrate

 import (
 	"fmt"

 	"github.com/lingyuins/octopus/internal/model"
 	"gorm.io/gorm"
 )

 func init() {
 	RegisterAfterAutoMigration(Migration{
 		Version: 57,
 		Up:      addGroupSortMultiplierColumns,
 	})
 }

 // 057: 分组排序策略与倍率上限（Seller 移植），一次加齐：
 // groups.sort_strategy、channels.is_reserve、
 // site_user_groups.multiplier/multiplier_known/policy_blocked/policy_block_reason/policy_blocked_at。
 // 既有行保持零值：排序跟随全局默认（空=non_relay_balance），倍率未知按 1x 放行，不阻断。
 func addGroupSortMultiplierColumns(database *gorm.DB) error {
 	if database == nil {
 		return fmt.Errorf("db is nil")
 	}
 	if database.Migrator().HasTable(&model.Group{}) &&
 		!database.Migrator().HasColumn(&model.Group{}, "SortStrategy") {
 		if err := database.Migrator().AddColumn(&model.Group{}, "SortStrategy"); err != nil {
 			return fmt.Errorf("add groups.sort_strategy: %w", err)
 		}
 	}
 	if database.Migrator().HasTable(&model.Channel{}) &&
 		!database.Migrator().HasColumn(&model.Channel{}, "IsReserve") {
 		if err := database.Migrator().AddColumn(&model.Channel{}, "IsReserve"); err != nil {
 			return fmt.Errorf("add channels.is_reserve: %w", err)
 		}
 	}
 	if database.Migrator().HasTable(&model.SiteUserGroup{}) {
 		for _, column := range []string{"Multiplier", "MultiplierKnown", "PolicyBlocked", "PolicyBlockReason", "PolicyBlockedAt"} {
 			if database.Migrator().HasColumn(&model.SiteUserGroup{}, column) {
 				continue
 			}
 			if err := database.Migrator().AddColumn(&model.SiteUserGroup{}, column); err != nil {
 				return fmt.Errorf("add site_user_groups.%s: %w", column, err)
 			}
 		}
 	}
 	return nil
 }
