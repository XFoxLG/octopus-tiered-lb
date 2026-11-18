package backup

import (
	"context"
	"fmt"
	"reflect"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm/clause"
)

// v6DeleteOrder is the reverse dependency order used for authoritative v6
// full restore deletion. A table is cleared only when its section appears in
// the backup's explicit manifest.
var v6DeleteOrder = []string{
	"stats_api_key", "stats_channel", "stats_model",
	"stats_hourly", "stats_daily", "stats_total",
	"group_items", "channel_groups", "groups",
	"notifications",
	"audit_logs", "runtime_states", "circuit_breaker_states",
	"api_keys", "channel_keys", "channels",
	"llm_infos", "settings",
}

// sectionTable maps a manifest section to the physical table it owns.
func sectionTable(section string) string {
	switch section {
	case "stats_total":
		return "stats_totals"
	case "stats_daily":
		return "stats_dailies"
	case "stats_hourly":
		return "stats_hourlies"
	case "stats_model":
		return "stats_models"
	case "stats_channel":
		return "stats_channels"
	case "stats_api_key":
		return "stats_api_keys"
	case "runtime_states":
		return "auto_strategy_states"
	case "audit_logs":
		return "audit_logs"
	case "error_logs":
		return "error_logs"
	}
	for _, s := range allSections() {
		if s.name == section {
			return s.table
		}
	}
	return ""
}

// deleteAuthoritativeSections clears only the collections the v6 backup
// explicitly owns. Missing sections keep their target rows.
func deleteAuthoritativeSections(cfg *importConfig, dump *model.DBDump, authority map[string]bool) error {
	for _, section := range v6DeleteOrder {
		table := sectionTable(section)
		if table == "" {
			return fmt.Errorf("backup section %q has no table mapping", section)
		}
		if !authority[section] {
			continue
		}
		if rows, ok := sectionRowsByName(dump, section); ok && isNullSection(rows) {
			continue
		}
		if !cfg.conn.Migrator().HasTable(table) {
			continue
		}
		if err := cfg.deleteAll(table); err != nil {
			return fmt.Errorf("full import: delete %s: %w", table, err)
		}
	}
	for _, section := range []string{
		"stats_daily_channel_model", "stats_daily_api_key",
		"stats_daily_model", "stats_daily_channel",
	} {
		if !authority[section] {
			continue
		}
		if rows, ok := sectionRowsByName(dump, section); ok && isNullSection(rows) {
			continue
		}
		table := sectionTableByName(section)
		if !cfg.conn.Migrator().HasTable(table) {
			continue
		}
		if err := cfg.deleteAll(table); err != nil {
			return fmt.Errorf("full import: delete %s: %w", section, err)
		}
	}
	for _, section := range []string{
		"channel_probe_results", "channel_probe_runs",
		"channel_model_capabilities", "channel_model_limits",
		"proxy_configurations", "model_mappings",
		"model_price_categories", "model_price_schedules",
		"api_credential_profiles",
		"group_health_attempts", "group_health_snapshots", "ai_route_tasks",
	} {
		if !authority[section] {
			continue
		}
		if rows, ok := sectionRowsByName(dump, section); ok && isNullSection(rows) {
			continue
		}
		if !cfg.conn.Migrator().HasTable(sectionTable(section)) {
			continue
		}
		if err := cfg.deleteAll(sectionTable(section)); err != nil {
			return fmt.Errorf("full import: delete %s: %w", section, err)
		}
	}
	if authority["error_logs"] && !isNullSection(&dump.ErrorLogs) && cfg.conn.Migrator().HasTable("error_logs") {
		if err := cfg.deleteAll("error_logs"); err != nil {
			return fmt.Errorf("full import: delete error_logs: %w", err)
		}
	}
	return nil
}

func sectionTableByName(name string) string {
	switch name {
	case "stats_daily_channel_model":
		return "stats_daily_channel_models"
	case "stats_daily_api_key":
		return "stats_daily_api_keys"
	case "stats_daily_model":
		return "stats_daily_models"
	case "stats_daily_channel":
		return "stats_daily_channels"
	}
	return name
}

// importV6Sections imports a v6 configuration dump with explicit section
// authority. Full restores use complete rows. Decoded incremental imports patch
// only explicit non-null fields, preserving omitted/null target values while
// writing explicit false, zero and empty strings exactly.
func importV6Sections(cfg *importConfig, dump *model.DBDump, authority map[string]bool, skipRelayLogs bool) error {
	if cfg.isFull {
		if err := validateChannelSectionDependencies(authority); err != nil {
			return err
		}
		if err := deleteAuthoritativeSections(cfg, dump, authority); err != nil {
			return err
		}
	}

	for _, s := range allSections() {
		if !authority[s.name] {
			continue
		}
		rows := s.rows(dump)
		if isNullSection(rows) {
			continue
		}
		var sourceIndexes []int
		if s.name == "settings" {
			rows = &dump.Settings
			filtered := dump.Settings[:0]
			sourceIndexes = make([]int, 0, len(dump.Settings))
			skipped := false
			for index, setting := range dump.Settings {
				if identityOwnedSetting(setting.Key) {
					skipped = true
					continue
				}
				filtered = append(filtered, setting)
				sourceIndexes = append(sourceIndexes, index)
			}
			dump.Settings = filtered
			if skipped {
				cfg.res.Warnings = append(cfg.res.Warnings,
					"Passkey/WebAuthn identity settings in the backup were ignored; the target administrator and Passkeys are unchanged")
			}
		}
		count := sectionRowCount(rows)
		if err := cfg.insertSectionExactWithIndexes(dump, s, rows, count, sourceIndexes); err != nil {
			return err
		}
	}

	if authority["stats_total"] && !isNullSection(&dump.StatsTotal) {
		s := section{name: "stats_total", table: "stats_totals", model: &model.StatsTotal{}, conflict: primaryKeyColumns(&model.StatsTotal{}, cfg.conn)}
		if err := cfg.insertSectionExact(dump, s, &dump.StatsTotal, len(dump.StatsTotal)); err != nil {
			return err
		}
	}
	if authority["stats_daily"] && !isNullSection(&dump.StatsDaily) {
		s := section{name: "stats_daily", table: "stats_dailies", model: &model.StatsDaily{}, conflict: []clause.Column{{Name: "date"}}}
		if err := cfg.insertSectionExact(dump, s, &dump.StatsDaily, len(dump.StatsDaily)); err != nil {
			return err
		}
	}
	if authority["stats_hourly"] && !isNullSection(&dump.StatsHourly) {
		s := section{name: "stats_hourly", table: "stats_hourlies", model: &model.StatsHourly{}, conflict: []clause.Column{{Name: "hour"}, {Name: "date"}}}
		if err := cfg.insertSectionExact(dump, s, &dump.StatsHourly, len(dump.StatsHourly)); err != nil {
			return err
		}
	}
	if authority["stats_model"] && !isNullSection(&dump.StatsModel) {
		s := section{name: "stats_model", table: "stats_models", model: &model.StatsModel{}, conflict: primaryKeyColumns(&model.StatsModel{}, cfg.conn)}
		if err := cfg.insertSectionExact(dump, s, &dump.StatsModel, len(dump.StatsModel)); err != nil {
			return err
		}
	}
	if authority["stats_channel"] && !isNullSection(&dump.StatsChannel) {
		s := section{name: "stats_channel", table: "stats_channels", model: &model.StatsChannel{}, conflict: []clause.Column{{Name: "channel_id"}}}
		if err := cfg.insertSectionExact(dump, s, &dump.StatsChannel, len(dump.StatsChannel)); err != nil {
			return err
		}
	}
	if authority["stats_api_key"] && !isNullSection(&dump.StatsAPIKey) {
		s := section{name: "stats_api_key", table: "stats_api_keys", model: &model.StatsAPIKey{}, conflict: []clause.Column{{Name: "api_key_id"}}}
		if err := cfg.insertSectionExact(dump, s, &dump.StatsAPIKey, len(dump.StatsAPIKey)); err != nil {
			return err
		}
	}
	if err := importV6DetailedStats(cfg, dump, authority); err != nil {
		return err
	}

	if authority["error_logs"] && !isNullSection(&dump.ErrorLogs) {
		if err := cfg.doNothing("error_logs", &dump.ErrorLogs, len(dump.ErrorLogs)); err != nil {
			return err
		}
	}
	if authority["audit_logs"] && !isNullSection(&dump.AuditLogs) {
		if err := cfg.doNothing("audit_logs", &dump.AuditLogs, len(dump.AuditLogs)); err != nil {
			return err
		}
	}
	if !skipRelayLogs && (authority["relay_logs"] || authority["relay_log_attempts"] || authority["relay_log_content_refs"] || authority["relay_log_content_blobs"]) {
		if err := importRelayLogFamily(cfg, dump); err != nil {
			return err
		}
	}
	return synchronizeImportedSequences(cfg)
}

// identityOwnedSetting marks settings that belong to the target deployment's
// administrator authentication. Restoring another instance's values would
// invalidate the Passkeys already bound here.
func identityOwnedSetting(key model.SettingKey) bool {
	switch key {
	case model.SettingKeyWebAuthnRPID, model.SettingKeyWebAuthnRPName, model.SettingKeyWebAuthnOrigins:
		return true
	}
	return false
}

// validateChannelSectionDependencies prevents a full restore from replacing
// the channel table while leaving live foreign-key collections pointing at
// another deployment's channel IDs.
func validateChannelSectionDependencies(authority map[string]bool) error {
	if !authority["channels"] {
		return nil
	}
	for _, dependent := range []string{"channel_keys", "group_items"} {
		if !authority[dependent] {
			return fmt.Errorf(
				"full restore replaces channels but does not own %q; include that section in the backup manifest or omit channels",
				dependent,
			)
		}
	}
	return nil
}

func sectionRowCount(rows any) int {
	value := reflect.ValueOf(rows)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return 0
	}
	value = value.Elem()
	if value.Kind() != reflect.Slice {
		return 0
	}
	return value.Len()
}

func importV6DetailedStats(cfg *importConfig, dump *model.DBDump, authority map[string]bool) error {
	tables := []struct {
		section string
		table   string
		model   any
		rows    any
		columns []clause.Column
	}{
		{"stats_daily_channel", "stats_daily_channels", &model.StatsDailyChannel{}, &dump.StatsDailyChannel, []clause.Column{{Name: "date"}, {Name: "channel_id"}}},
		{"stats_daily_model", "stats_daily_models", &model.StatsDailyModel{}, &dump.StatsDailyModel, []clause.Column{{Name: "date"}, {Name: "model_name"}}},
		{"stats_daily_api_key", "stats_daily_api_keys", &model.StatsDailyAPIKey{}, &dump.StatsDailyAPIKey, []clause.Column{{Name: "date"}, {Name: "api_key_id"}}},
		{"stats_daily_channel_model", "stats_daily_channel_models", &model.StatsDailyChannelModel{}, &dump.StatsDailyChannelModel, []clause.Column{{Name: "date"}, {Name: "channel_id"}, {Name: "model_name"}}},
	}
	for _, row := range tables {
		if !authority[row.section] {
			continue
		}
		if isNullSection(row.rows) {
			continue
		}
		s := section{name: row.section, table: row.table, model: row.model, conflict: row.columns}
		if err := cfg.insertSectionExact(dump, s, row.rows, sectionRowCount(row.rows)); err != nil {
			return err
		}
	}
	return nil
}

// PreviewChannelMapping resolves v6 channel identities without writing. It is
// the dry-run counterpart used by the management import endpoint.
func PreviewChannelMapping(ctx context.Context, dump *model.DBDump, mode string) ([]model.DBChannelMapping, error) {
	if dump == nil {
		return nil, fmt.Errorf("empty dump")
	}
	if mode != model.ImportModeFull && mode != model.ImportModeIncremental {
		return nil, fmt.Errorf("unsupported import mode")
	}
	authority, err := authoritativeSections(dump)
	if err != nil {
		return nil, err
	}
	preserveMissingUIDs := missingChannelUIDPatchRows(dump)
	missingUID := make(map[int]bool, len(dump.Channels))
	for index, channel := range dump.Channels {
		if channel.ChannelUID == "" && !preserveMissingUIDs[index] {
			missingUID[channel.ID] = true
		}
	}
	if err := ensureDumpChannelUIDs(dump, preserveMissingUIDs); err != nil {
		return nil, err
	}
	_, mappings, err := channelIDMapping(ctx, db.GetDB(), dump, mode == model.ImportModeFull, authority, nil, missingUID)
	return mappings, err
}
