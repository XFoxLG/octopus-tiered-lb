package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 62,
		Up:      addChannelCapacityLimits,
	})
}

// 062: 为渠道增加容量级配额：max_concurrency（同时在途 upstream 请求上限）
// 与 rpm_limit（每分钟请求数上限）。既有行保持 0（= 不限制），行为不变。
// 共库多实例部署的迁移互斥由 Migrate 入口的 Postgres advisory lock 保证。
func addChannelCapacityLimits(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.Channel{}) {
		return nil
	}
	if !database.Migrator().HasColumn(&model.Channel{}, "MaxConcurrency") {
		if err := database.Migrator().AddColumn(&model.Channel{}, "MaxConcurrency"); err != nil {
			return fmt.Errorf("add channels.max_concurrency: %w", err)
		}
	}
	if !database.Migrator().HasColumn(&model.Channel{}, "RPMLimit") {
		if err := database.Migrator().AddColumn(&model.Channel{}, "RPMLimit"); err != nil {
			return fmt.Errorf("add channels.rpm_limit: %w", err)
		}
	}
	return nil
}
