package backup

import (
	"database/sql"
	"fmt"
	"reflect"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// Explicit IDs in a JSON restore do not advance PostgreSQL serial sequences.
// Advance only sequences belonging to imported tables, never lower a sequence
// that has already allocated a larger ID. Database archives preserve sequences
// themselves, but application JSON backups need this step before new writes.
func synchronizeImportedSequences(cfg *importConfig) error {
	if cfg.conn.Dialector.Name() != "postgres" {
		return nil
	}
	seen := make(map[string]bool)
	for _, step := range cfg.res.Progress {
		if step.Mode == "delete" || step.RowsAffected == 0 || seen[step.Table] {
			continue
		}
		seen[step.Table] = true
		var sequence sql.NullString
		if err := cfg.conn.Raw(`SELECT pg_get_serial_sequence(?, column_name)
FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = ? AND column_name = 'id'`, step.Table, step.Table).Scan(&sequence).Error; err != nil {
			return fmt.Errorf("restore %s sequence lookup: %w", step.Table, err)
		}
		if !sequence.Valid || sequence.String == "" {
			continue
		}
		var maximum int64
		if err := cfg.conn.Raw("SELECT COALESCE(MAX(id), 0) FROM " + quoteTableName(cfg.conn, step.Table)).Scan(&maximum).Error; err != nil {
			return err
		}
		if maximum == 0 {
			continue
		}
		if err := cfg.conn.Exec("SELECT setval(?::regclass, GREATEST(?, COALESCE(pg_sequence_last_value(?::regclass), 0)), true)", sequence.String, maximum, sequence.String).Error; err != nil {
			return fmt.Errorf("restore %s sequence: %w", step.Table, err)
		}
	}
	return nil
}

// These tables are application configuration/evidence. Auth identities,
// deployment credentials, migration records and transient connection affinity
// remain owned by the target deployment.
func exportExtendedConfiguration(conn *gorm.DB, dump *model.DBDump) error {
	rows := []struct {
		name string
		dest any
	}{
		{"proxy_configurations", &dump.ProxyConfigurations},
		{"model_mappings", &dump.ModelMappings},
		{"model_price_categories", &dump.ModelPriceCategories},
		{"model_price_schedules", &dump.ModelPriceSchedules},
		{"channel_model_capabilities", &dump.ChannelModelCapabilities},
		{"channel_model_limits", &dump.ChannelModelLimits},
		{"channel_probe_runs", &dump.ChannelProbeRuns},
		{"channel_probe_results", &dump.ChannelProbeResults},
	}
	for _, row := range rows {
		if err := conn.Find(row.dest).Error; err != nil {
			return fmt.Errorf("export %s: %w", row.name, err)
		}
	}
	return nil
}

func exportDetailedStats(conn *gorm.DB, dump *model.DBDump) error {
	for _, row := range []struct {
		name string
		dest any
	}{
		{"stats_daily_channels", &dump.StatsDailyChannel},
		{"stats_daily_models", &dump.StatsDailyModel},
		{"stats_daily_api_keys", &dump.StatsDailyAPIKey},
		{"stats_daily_channel_models", &dump.StatsDailyChannelModel},
	} {
		if err := conn.Find(row.dest).Error; err != nil {
			return fmt.Errorf("export %s: %w", row.name, err)
		}
	}
	return nil
}

func deleteExtendedTables(cfg *importConfig, dump *model.DBDump) error {
	// A historical dump cannot authoritatively replace tables it never exported.
	// v4 covers all these tables, including intentionally empty configuration.
	tables := []struct {
		name    string
		present bool
	}{
		{"channel_probe_results", len(dump.ChannelProbeResults) > 0},
		{"channel_probe_runs", len(dump.ChannelProbeRuns) > 0},
		{"channel_model_capabilities", len(dump.ChannelModelCapabilities) > 0},
		{"channel_model_limits", len(dump.ChannelModelLimits) > 0},
		{"proxy_configurations", len(dump.ProxyConfigurations) > 0},
		{"model_mappings", len(dump.ModelMappings) > 0},
		{"model_price_categories", len(dump.ModelPriceCategories) > 0},
		{"model_price_schedules", len(dump.ModelPriceSchedules) > 0},
		{"api_credential_profiles", len(dump.APICredentialProfiles) > 0},
	}
	for _, table := range tables {
		if dump.Version >= 4 || table.present {
			if err := cfg.deleteAll(table.name); err != nil {
				return fmt.Errorf("full import: delete %s: %w", table.name, err)
			}
		}
	}
	if dump.IncludeStats && dump.Version >= 4 {
		for _, table := range []string{"stats_daily_channel_models", "stats_daily_api_keys", "stats_daily_models", "stats_daily_channels"} {
			if err := cfg.deleteAll(table); err != nil {
				return err
			}
		}
	}
	if dump.IncludeLogs && dump.Version >= 4 {
		return cfg.deleteAll("error_logs")
	}
	return nil
}

func importExtendedConfiguration(cfg *importConfig, dump *model.DBDump) error {
	for _, row := range []struct {
		name string
		rows any
	}{
		{"proxy_configurations", &dump.ProxyConfigurations},
		{"model_mappings", &dump.ModelMappings},
		{"model_price_categories", &dump.ModelPriceCategories},
		{"model_price_schedules", &dump.ModelPriceSchedules},
	} {
		if err := cfg.insertExactValues(row.name, row.rows, clause.OnConflict{DoNothing: true}); err != nil {
			return err
		}
	}
	return nil
}

func importProbeEvidence(cfg *importConfig, dump *model.DBDump) error {
	for _, row := range []struct {
		name string
		rows any
	}{
		{"channel_model_capabilities", &dump.ChannelModelCapabilities},
		{"channel_model_limits", &dump.ChannelModelLimits},
		{"channel_probe_runs", &dump.ChannelProbeRuns},
		{"channel_probe_results", &dump.ChannelProbeResults},
	} {
		if err := cfg.insertExactValues(row.name, row.rows, clause.OnConflict{DoNothing: true}); err != nil {
			return err
		}
	}
	return nil
}

func importDetailedStats(cfg *importConfig, dump *model.DBDump) error {
	for _, row := range []struct {
		name    string
		rows    any
		columns []clause.Column
	}{
		{"stats_daily_channels", &dump.StatsDailyChannel, []clause.Column{{Name: "date"}, {Name: "channel_id"}}},
		{"stats_daily_models", &dump.StatsDailyModel, []clause.Column{{Name: "date"}, {Name: "model_name"}}},
		{"stats_daily_api_keys", &dump.StatsDailyAPIKey, []clause.Column{{Name: "date"}, {Name: "api_key_id"}}},
		{"stats_daily_channel_models", &dump.StatsDailyChannelModel, []clause.Column{{Name: "date"}, {Name: "channel_id"}, {Name: "model_name"}}},
	} {
		if err := cfg.insertExactValues(row.name, row.rows, clause.OnConflict{Columns: row.columns, UpdateAll: true}); err != nil {
			return err
		}
	}
	return nil
}

// Map-based inserts avoid GORM replacing saved false/0 values with non-zero
// model defaults (disabled proxies, zero price multipliers and time windows).
// These extended models contain scalar database columns; association fields
// have no DBName and are never inserted by this path.
func (cfg *importConfig) insertExactValues(table string, rows any, conflict clause.OnConflict) error {
	slice := reflect.ValueOf(rows).Elem()
	if slice.Len() == 0 {
		return nil
	}
	parsed, err := schema.Parse(reflect.New(slice.Type().Elem()).Interface(), &batchSchemaCache, cfg.conn.NamingStrategy)
	if err != nil {
		return err
	}
	batchSize := maxSQLVariablesPerStatement / len(parsed.DBNames)
	if batchSize < 1 {
		batchSize = 1
	}
	var affected int64
	for offset := 0; offset < slice.Len(); offset += batchSize {
		end := min(offset+batchSize, slice.Len())
		batch := make([]map[string]any, 0, end-offset)
		for index := offset; index < end; index++ {
			values := make(map[string]any, len(parsed.DBNames))
			for _, name := range parsed.DBNames {
				field := parsed.FieldsByDBName[name]
				value, _ := field.ValueOf(cfg.conn.Statement.Context, slice.Index(index))
				values[name] = value
			}
			batch = append(batch, values)
		}
		// For maps GORM has no model primary keys to infer UpdateAll. Supply the
		// exact non-conflict column list explicitly for detailed stats upserts.
		batchConflict := conflict
		if batchConflict.UpdateAll {
			protected := make(map[string]bool)
			for _, column := range conflict.Columns {
				protected[column.Name] = true
			}
			var updateNames []string
			for _, name := range parsed.DBNames {
				if !protected[name] {
					updateNames = append(updateNames, name)
				}
			}
			batchConflict.UpdateAll = false
			batchConflict.DoUpdates = clause.AssignmentColumns(updateNames)
		}
		result := cfg.conn.Table(table).Clauses(batchConflict).Create(&batch)
		if result.Error != nil {
			appendStep(cfg.res, table, "insert", affected, result.Error)
			return fmt.Errorf("%s: batch [%d:%d]: %w", table, offset, end, result.Error)
		}
		affected += result.RowsAffected
	}
	appendStep(cfg.res, table, "insert", affected, nil)
	return nil
}
