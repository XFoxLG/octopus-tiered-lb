package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 59,
		Up:      dropSiteHubTables,
	})
}

// 059: site / hub（远程站点）子系统整体移除后的清理迁移。
//
// 删除 site 管理链（sites 及其子表、投影渠道绑定、站点模型小时统计）、
// hub 远程站点链（remote_sites 及余额快照/签到记录/公告/令牌/兑换码/用量历史）、
// 以及第 4 步号池移除时承诺后续 DROP 的 account pool / plan provider 表。
//
// 同时移除列：groups.sort_strategy 与 channels.is_reserve（Seller 排序策略，
// 已随 group_sort 逻辑删除）、channel_keys.managed（site 投影 key 标记）。
// 迁移在 AfterAutoMigrate 阶段执行，此时 AutoMigrate 已按精简后的模型建表；
// 旧库中的遗留表不会出现在模型列表里，必须显式 DROP。幂等：先 HasTable/HasColumn
// 再删，重复执行安全。
func dropSiteHubTables(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}

	// 子表在前、父表在后，避免外键约束阻止删除。
	tablesToDrop := []string{
		"site_channel_bindings",
		"site_models",
		"site_user_groups",
		"site_tokens",
		"site_accounts",
		"sites",
		"site_prices",
		"stats_site_model_hourlies",
		"balance_snapshots",
		"check_in_records",
		"site_announcements",
		"remote_site_tokens",
		"redemption_records",
		"remote_usage_records",
		"remote_sites",
		"pool_accounts",
		"account_pools",
		"plan_providers",
	}
	for _, table := range tablesToDrop {
		if !db.Migrator().HasTable(table) {
			continue
		}
		if err := db.Migrator().DropTable(table); err != nil {
			return fmt.Errorf("drop table %s: %w", table, err)
		}
	}

	columnDrops := []struct {
		table  string
		column string
	}{
		{"groups", "sort_strategy"},
		{"channels", "is_reserve"},
		{"channels", "pool_id"},
		{"channel_keys", "managed"},
	}
	for _, drop := range columnDrops {
		if !db.Migrator().HasTable(drop.table) {
			continue
		}
		if !db.Migrator().HasColumn(drop.table, drop.column) {
			continue
		}
		if err := db.Migrator().DropColumn(drop.table, drop.column); err != nil {
			return fmt.Errorf("drop column %s.%s: %w", drop.table, drop.column, err)
		}
	}

	return nil
}
