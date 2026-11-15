package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 84,
		Up:      addChannelProbeEvidence,
	})
}

// 084 adds structured evidence to manual probe results. The column is additive
// and nullable: old rows remain unknown instead of receiving inferred protocol,
// timing, or terminal values.
func addChannelProbeEvidence(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.ChannelProbeResult{}) {
		return nil
	}
	if database.Migrator().HasColumn(&model.ChannelProbeResult{}, "Evidence") {
		return nil
	}
	if err := database.Migrator().AddColumn(&model.ChannelProbeResult{}, "Evidence"); err != nil {
		return fmt.Errorf("add channel_probe_results.evidence: %w", err)
	}
	return nil
}
