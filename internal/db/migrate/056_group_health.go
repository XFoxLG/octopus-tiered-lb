package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 56,
		Up:      addGroupHealthTables,
	})
}

// 056: 新建分组健康检查快照双表（Seller 移植）。AutoMigrate 已建表时为空操作。
func addGroupHealthTables(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.GroupHealthSnapshot{}) {
		if err := database.Migrator().CreateTable(&model.GroupHealthSnapshot{}); err != nil {
			return fmt.Errorf("create group_health_snapshots: %w", err)
		}
	}
	if !database.Migrator().HasTable(&model.GroupHealthAttempt{}) {
		if err := database.Migrator().CreateTable(&model.GroupHealthAttempt{}); err != nil {
			return fmt.Errorf("create group_health_attempts: %w", err)
		}
	}
	return nil
}
