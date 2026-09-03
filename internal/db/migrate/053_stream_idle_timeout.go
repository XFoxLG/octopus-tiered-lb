package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 53,
		Up:      addStreamIdleTimeoutToGroups,
	})
}

// 053: adds an opt-in post-visible-output stream idle watchdog to groups.
// Existing rows receive the database default of zero, which keeps the watchdog
// disabled until an administrator explicitly configures the group.
func addStreamIdleTimeoutToGroups(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.Group{}) {
		return nil
	}
	if database.Migrator().HasColumn(&model.Group{}, "StreamIdleTimeout") {
		return nil
	}
	if err := database.Migrator().AddColumn(&model.Group{}, "StreamIdleTimeout"); err != nil {
		return fmt.Errorf("add groups.stream_idle_timeout: %w", err)
	}
	return nil
}
