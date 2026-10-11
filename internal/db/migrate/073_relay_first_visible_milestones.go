package migrate

import (
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 73, Up: addRelayFirstVisibleMilestones})
}

// Additive only: older binaries can still read/write logs; rollback leaves
// these unused columns in place, preserving both existing and new trace data.
func addRelayFirstVisibleMilestones(database *gorm.DB) error {
	if !database.Migrator().HasTable(&model.RelayLog{}) {
		return nil
	}
	for _, field := range []string{"UpstreamFirstVisibleAt", "ClientFirstFlushAt"} {
		if !database.Migrator().HasColumn(&model.RelayLog{}, field) {
			if err := database.Migrator().AddColumn(&model.RelayLog{}, field); err != nil {
				return err
			}
		}
	}
	return nil
}
