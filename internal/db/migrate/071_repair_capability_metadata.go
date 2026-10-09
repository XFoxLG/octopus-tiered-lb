package migrate

import (
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 71, Up: repairCapabilityMetadata})
}

func repairCapabilityMetadata(database *gorm.DB) error {
	if !database.Migrator().HasTable(&model.ChannelModelCapability{}) ||
		!database.Migrator().HasTable(&model.GroupItem{}) ||
		!database.Migrator().HasColumn(&model.GroupItem{}, "SupportsToolsProbedAt") {
		return nil
	}
	return migrateGroupItemToolsCapabilityWithRepair(database, true)
}
