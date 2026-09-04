package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 58,
		Up:      addGroupReasoningPolicyColumns,
	})
}

// 058: 分组默认思考档位（default_reasoning_effort）与强制覆盖显式关闭
// （reasoning_force_override）。既有行保持零值：不注入、不强制，
// 行为与迁移前逐字一致。
func addGroupReasoningPolicyColumns(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.Group{}) {
		return nil
	}
	for _, column := range []string{"DefaultReasoningEffort", "ReasoningForceOverride"} {
		if database.Migrator().HasColumn(&model.Group{}, column) {
			continue
		}
		if err := database.Migrator().AddColumn(&model.Group{}, column); err != nil {
			return fmt.Errorf("add groups.%s: %w", column, err)
		}
	}
	return nil
}
