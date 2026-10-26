package migrate

import (
	"fmt"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 80, Up: migrateRelayRequestLifecycle})
}

// Additive and idempotent: no existing group, Key, log or migration record is
// removed or assigned a purpose-specific budget.
func migrateRelayRequestLifecycle(database *gorm.DB) error {
	for _, table := range []struct {
		model  any
		fields []string
	}{
		{&model.Group{}, []string{"RequestTimeoutSeconds", "StreamKeepaliveEnabled", "AssistantPrefillCompat"}},
		{&model.APIKey{}, []string{"RequestTimeoutSeconds"}},
		{&model.RelayLog{}, []string{"Diagnostics"}},
	} {
		if !database.Migrator().HasTable(table.model) {
			continue
		}
		for _, field := range table.fields {
			if !database.Migrator().HasColumn(table.model, field) {
				if err := database.Migrator().AddColumn(table.model, field); err != nil {
					return fmt.Errorf("add lifecycle field %s: %w", field, err)
				}
			}
		}
	}
	return nil
}
