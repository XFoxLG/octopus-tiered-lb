package migrate

import (
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() { RegisterAfterAutoMigration(Migration{Version: 74, Up: addGroupHedgeConfig}) }
func addGroupHedgeConfig(database *gorm.DB) error {
	if !database.Migrator().HasTable(&model.Group{}) {
		return nil
	}
	if !database.Migrator().HasColumn(&model.Group{}, "HedgeEnabled") {
		if err := database.Migrator().AddColumn(&model.Group{}, "HedgeEnabled"); err != nil {
			return err
		}
	}
	if !database.Migrator().HasColumn(&model.Group{}, "HedgeDelayMs") {
		if err := database.Migrator().AddColumn(&model.Group{}, "HedgeDelayMs"); err != nil {
			return err
		}
	}
	return nil
}
