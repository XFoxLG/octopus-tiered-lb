package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 83,
		Up:      addNonChatAccountingColumns,
	})
}

var nonChatMetricFields = []string{
	"UnattributedToken",
	"BilledToken",
	"UsageReportedRequests",
	"UsagePartialRequests",
	"UsageBilledRequests",
	"UsageInvalidRequests",
	"EmbeddingInputItems",
	"EmbeddingInputObserved",
	"EmbeddingVectors",
	"EmbeddingVectorsObserved",
	"RerankDocuments",
	"RerankDocumentsObserved",
	"RerankResults",
	"RerankResultsObserved",
}

// 083: add the non-chat usage evidence columns used by all statistics tables
// and relay_logs. History stays zero because previous binaries had no way to
// distinguish "absent" from "explicitly zero". The columns are additive so an
// older binary can continue to read and write the surrounding rows.
func addNonChatAccountingColumns(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}

	tables := []any{
		&model.StatsTotal{},
		&model.StatsDaily{},
		&model.StatsDailyChannel{},
		&model.StatsDailyModel{},
		&model.StatsDailyAPIKey{},
		&model.StatsDailyChannelModel{},
		&model.StatsHourly{},
		&model.StatsModel{},
		&model.StatsChannel{},
		&model.StatsAPIKey{},
		&model.RelayLog{},
	}

	for _, table := range tables {
		if !database.Migrator().HasTable(table) {
			continue
		}
		for _, field := range nonChatMetricFields {
			if database.Migrator().HasColumn(table, field) {
				continue
			}
			if err := database.Migrator().AddColumn(table, field); err != nil {
				return fmt.Errorf("add %T.%s: %w", table, field, err)
			}
		}
	}
	if database.Migrator().HasTable(&model.RelayLog{}) && !database.Migrator().HasColumn(&model.RelayLog{}, "NonChatUsage") {
		return database.Migrator().AddColumn(&model.RelayLog{}, "NonChatUsage")
	}
	return nil
}
