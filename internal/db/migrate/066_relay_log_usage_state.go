package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 66,
		Up:      addRelayLogUsageState,
	})
}

// 066: relay_logs.usage_state —— 日志用量可信度标注。上游不回报 usage、流中断、
// 客户端断连、空输出都会造成"未知"，但成因不同；该列由写入端按成因填写，
// 既有行为为空（历史行无标注，前端按现状兜底）。可空字符串，无默认值。
func addRelayLogUsageState(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.RelayLog{}) {
		return nil
	}
	if !database.Migrator().HasColumn(&model.RelayLog{}, "UsageState") {
		if err := database.Migrator().AddColumn(&model.RelayLog{}, "UsageState"); err != nil {
			return fmt.Errorf("add relay_logs.usage_state: %w", err)
		}
	}
	return nil
}
