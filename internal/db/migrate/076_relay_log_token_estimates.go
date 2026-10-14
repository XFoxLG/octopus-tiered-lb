package migrate

import (
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 76, Up: addRelayLogTokenEstimates})
}

// Additive only: older binaries can still read/write logs; rollback leaves
// these unused columns in place, preserving existing and new trace data.
// 0 = 未估算（见 internal/relay/usage_estimate.go，v2.8.13）。
func addRelayLogTokenEstimates(database *gorm.DB) error {
	if !database.Migrator().HasTable(&model.RelayLog{}) {
		return nil
	}
	for _, field := range []string{"InputTokensEst", "OutputTokensEst"} {
		if !database.Migrator().HasColumn(&model.RelayLog{}, field) {
			if err := database.Migrator().AddColumn(&model.RelayLog{}, field); err != nil {
				return err
			}
		}
	}
	return nil
}
