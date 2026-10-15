package migrate

import (
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 79, Up: addRelayLogUpstreamModel})
}

// Additive only: v2.9.0 记录上游响应自报模型（用于「模型不一致」标记，
// 识别上游偷换/降级模型；空串表示未回报）。老版本二进制忽略该列。
func addRelayLogUpstreamModel(database *gorm.DB) error {
	if !database.Migrator().HasTable(&model.RelayLog{}) {
		return nil
	}
	if !database.Migrator().HasColumn(&model.RelayLog{}, "UpstreamModel") {
		return database.Migrator().AddColumn(&model.RelayLog{}, "UpstreamModel")
	}
	return nil
}
