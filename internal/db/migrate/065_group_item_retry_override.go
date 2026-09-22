package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 65,
		Up:      addGroupItemRetryOverride,
	})
}

// 065: group_items.relay_retry_count_override —— 条目级 Key 重试次数覆盖。
// NULL = 跟随渠道/分组/全局（与渠道级 -1 哨兵不同，条目级用 NULL 表"跟随"）；
// 0 = 该条目候选不重试；>0 = 最多重试 N 次。可空列，无默认值，既有行保持
// NULL 行为不变。共库多实例部署的迁移互斥由 Migrate 入口的 Postgres
// advisory lock 保证。
func addGroupItemRetryOverride(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.GroupItem{}) {
		return nil
	}
	if !database.Migrator().HasColumn(&model.GroupItem{}, "RelayRetryCountOverride") {
		if err := database.Migrator().AddColumn(&model.GroupItem{}, "RelayRetryCountOverride"); err != nil {
			return fmt.Errorf("add group_items.relay_retry_count_override: %w", err)
		}
	}
	return nil
}
