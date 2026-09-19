package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 63,
		Up:      addRelayRetryOverrides,
	})
}

// 063: 渠道级 Key 重试覆盖（relay_retry_count_override）与分组级 Key 重试
// （relay_retry_count）、路由轮次（relay_route_retries）。既有行保持 -1
// （= 跟随分组/全局），行为不变。共库多实例部署的迁移互斥由 Migrate 入口的
// Postgres advisory lock 保证。
func addRelayRetryOverrides(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if database.Migrator().HasTable(&model.Channel{}) && !database.Migrator().HasColumn(&model.Channel{}, "RelayRetryCountOverride") {
		if err := database.Migrator().AddColumn(&model.Channel{}, "RelayRetryCountOverride"); err != nil {
			return fmt.Errorf("add channels.relay_retry_count_override: %w", err)
		}
	}
	if !database.Migrator().HasTable(&model.Group{}) {
		return nil
	}
	if !database.Migrator().HasColumn(&model.Group{}, "RelayRetryCount") {
		if err := database.Migrator().AddColumn(&model.Group{}, "RelayRetryCount"); err != nil {
			return fmt.Errorf("add groups.relay_retry_count: %w", err)
		}
	}
	if !database.Migrator().HasColumn(&model.Group{}, "RelayRouteRetries") {
		if err := database.Migrator().AddColumn(&model.Group{}, "RelayRouteRetries"); err != nil {
			return fmt.Errorf("add groups.relay_route_retries: %w", err)
		}
	}
	return nil
}
