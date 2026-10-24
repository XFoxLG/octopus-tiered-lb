package migrate

import (
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() { RegisterAfterAutoMigration(Migration{Version: 81, Up: migrateGroupForwardMode}) }

func migrateGroupForwardMode(database *gorm.DB) error {
	if !database.Migrator().HasTable(&model.Group{}) || database.Migrator().HasColumn(&model.Group{}, "ForwardMode") {
		return nil
	}
	return database.Migrator().AddColumn(&model.Group{}, "ForwardMode")
}
