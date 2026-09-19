package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 64,
		Up:      addChannelCircuitAndKeyCooldownOverrides,
	})
}

// 064: 渠道级断路器覆盖（circuit_breaker_threshold / cooldown / max_cooldown）
// 与渠道级 Key 冷却覆盖（key_cooldown_ratelimit / auth_error / server_error）。
// 既有行保持 0（= 跟随全局设置），行为不变。共库多实例部署的迁移互斥由
// Migrate 入口的 Postgres advisory lock 保证。
func addChannelCircuitAndKeyCooldownOverrides(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.Channel{}) {
		return nil
	}
	for _, column := range []struct {
		field string
		name  string
	}{
		{"CircuitBreakerThreshold", "channels.circuit_breaker_threshold"},
		{"CircuitBreakerCooldown", "channels.circuit_breaker_cooldown"},
		{"CircuitBreakerMaxCooldown", "channels.circuit_breaker_max_cooldown"},
		{"KeyCooldownRatelimit", "channels.key_cooldown_ratelimit"},
		{"KeyCooldownAuthError", "channels.key_cooldown_auth_error"},
		{"KeyCooldownServerError", "channels.key_cooldown_server_error"},
	} {
		if database.Migrator().HasColumn(&model.Channel{}, column.field) {
			continue
		}
		if err := database.Migrator().AddColumn(&model.Channel{}, column.field); err != nil {
			return fmt.Errorf("add %s: %w", column.name, err)
		}
	}
	return nil
}
