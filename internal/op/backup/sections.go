package backup

import (
	"context"
	"fmt"
	"reflect"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// section describes one backup collection whose authority is declared by
// DBDump.Sections in format v6.
type section struct {
	name  string // JSON section name, also the manifest identifier
	table string // physical table name (before naming strategy)
	model any    // model used for schema/table resolution
	rows  func(*model.DBDump) any
	// conflict columns for incremental upsert. Nil means the table has no
	// stable natural key beyond its primary key, supplied per row by GORM.
	conflict []clause.Column
}

func allSections() []section {
	return []section{
		{name: "channels", table: "channels", model: &model.Channel{}, rows: func(d *model.DBDump) any { return &d.Channels }},
		{name: "channel_keys", table: "channel_keys", model: &model.ChannelKey{}, rows: func(d *model.DBDump) any { return &d.ChannelKeys }},
		{name: "channel_groups", table: "channel_groups", model: &model.ChannelGroup{}, rows: func(d *model.DBDump) any { return &d.ChannelGroups }},
		{name: "groups", table: "groups", model: &model.Group{}, rows: func(d *model.DBDump) any { return &d.Groups }},
		{name: "group_items", table: "group_items", model: &model.GroupItem{}, rows: func(d *model.DBDump) any { return &d.GroupItems }},
		{name: "llm_infos", table: "llm_infos", model: &model.LLMInfo{}, rows: func(d *model.DBDump) any { return &d.LLMInfos }, conflict: []clause.Column{{Name: "name"}}},
		{name: "api_keys", table: "api_keys", model: &model.APIKey{}, rows: func(d *model.DBDump) any { return &d.APIKeys }},
		{name: "settings", table: "settings", model: &model.Setting{}, rows: func(d *model.DBDump) any { return &d.Settings }, conflict: []clause.Column{{Name: "key"}}},
		{name: "notifications", table: "notifications", model: &model.Notification{}, rows: func(d *model.DBDump) any { return &d.Notifications }},
		{name: "runtime_states", table: "auto_strategy_states", model: &model.AutoStrategyState{}, rows: func(d *model.DBDump) any { return &d.RuntimeStates }},
		{name: "circuit_breaker_states", table: "circuit_breaker_states", model: &model.CircuitBreakerState{}, rows: func(d *model.DBDump) any { return &d.CircuitBreakerStates }},
		{name: "proxy_configurations", table: "proxy_configurations", model: &model.ProxyConfiguration{}, rows: func(d *model.DBDump) any { return &d.ProxyConfigurations }},
		{name: "model_mappings", table: "model_mappings", model: &model.ModelMapping{}, rows: func(d *model.DBDump) any { return &d.ModelMappings }},
		{name: "model_price_categories", table: "model_price_categories", model: &model.ModelPriceCategory{}, rows: func(d *model.DBDump) any { return &d.ModelPriceCategories }},
		{name: "model_price_schedules", table: "model_price_schedules", model: &model.ModelPriceSchedule{}, rows: func(d *model.DBDump) any { return &d.ModelPriceSchedules }},
		{name: "channel_model_capabilities", table: "channel_model_capabilities", model: &model.ChannelModelCapability{}, rows: func(d *model.DBDump) any { return &d.ChannelModelCapabilities }},
		{name: "channel_model_limits", table: "channel_model_limits", model: &model.ChannelModelLimit{}, rows: func(d *model.DBDump) any { return &d.ChannelModelLimits }},
		{name: "channel_probe_runs", table: "channel_probe_runs", model: &model.ChannelProbeRun{}, rows: func(d *model.DBDump) any { return &d.ChannelProbeRuns }},
		{name: "channel_probe_results", table: "channel_probe_results", model: &model.ChannelProbeResult{}, rows: func(d *model.DBDump) any { return &d.ChannelProbeResults }},
		{name: "api_credential_profiles", table: "api_credential_profiles", model: &model.APICredentialProfile{}, rows: func(d *model.DBDump) any { return &d.APICredentialProfiles }},
		{name: "group_health_snapshots", table: "group_health_snapshots", model: &model.GroupHealthSnapshot{}, rows: func(d *model.DBDump) any { return &d.GroupHealthSnapshots }},
		{name: "group_health_attempts", table: "group_health_attempts", model: &model.GroupHealthAttempt{}, rows: func(d *model.DBDump) any { return &d.GroupHealthAttempts }},
		{name: "ai_route_tasks", table: "ai_route_tasks", model: &model.AIRouteTask{}, rows: func(d *model.DBDump) any { return &d.AIRouteTasks }},
	}
}

func modelForSection(name string) any {
	for _, s := range allSections() {
		if s.name == name {
			return s.model
		}
	}
	return nil
}

// normalizeDumpSlices makes every collection an explicit JSON array in a v6
// export. An empty exported collection therefore clears that collection on a
// full restore, while an absent/null key means "leave the target untouched".
func normalizeDumpSlices(dump *model.DBDump) {
	if dump == nil {
		return
	}
	value := reflect.ValueOf(dump).Elem()
	for i := 0; i < value.NumField(); i++ {
		if value.Type().Field(i).Name == "Users" {
			continue
		}
		field := value.Field(i)
		if field.Kind() == reflect.Slice && field.CanSet() && field.IsNil() {
			field.Set(reflect.MakeSlice(field.Type(), 0, 0))
		}
	}
}

// isNullSection reports whether a collection was decoded from JSON null (or
// was never supplied). An explicit empty array decodes as a non-nil empty
// slice and is therefore distinct.
func isNullSection(rows any) bool {
	value := reflect.ValueOf(rows)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return true
	}
	value = value.Elem()
	return value.Kind() == reflect.Slice && value.IsNil()
}

func sectionRowsByName(dump *model.DBDump, name string) (any, bool) {
	for _, s := range allSections() {
		if s.name == name {
			return s.rows(dump), true
		}
	}
	switch name {
	case "stats_total":
		return &dump.StatsTotal, true
	case "stats_daily":
		return &dump.StatsDaily, true
	case "stats_hourly":
		return &dump.StatsHourly, true
	case "stats_model":
		return &dump.StatsModel, true
	case "stats_channel":
		return &dump.StatsChannel, true
	case "stats_api_key":
		return &dump.StatsAPIKey, true
	case "stats_daily_channel":
		return &dump.StatsDailyChannel, true
	case "stats_daily_model":
		return &dump.StatsDailyModel, true
	case "stats_daily_api_key":
		return &dump.StatsDailyAPIKey, true
	case "stats_daily_channel_model":
		return &dump.StatsDailyChannelModel, true
	case "error_logs":
		return &dump.ErrorLogs, true
	case "audit_logs":
		return &dump.AuditLogs, true
	case "relay_logs":
		return &dump.RelayLogs, true
	case "relay_log_attempts":
		return &dump.RelayLogAttempts, true
	case "relay_log_content_refs":
		return &dump.RelayLogContentRefs, true
	case "relay_log_content_blobs":
		return &dump.RelayLogContentBlobs, true
	}
	return nil, false
}

func sectionNull(dump *model.DBDump, name string) bool {
	rows, ok := sectionRowsByName(dump, name)
	return ok && isNullSection(rows)
}

// dumpSectionNames lists the collections an exported dump authoritatively
// covers. Statistics and log sections are included only when requested.
func dumpSectionNames(d *model.DBDump) []string {
	names := make([]string, 0, len(allSections())+8)
	for _, s := range allSections() {
		names = append(names, s.name)
	}
	if d.IncludeStats {
		names = append(names,
			"stats_total", "stats_daily", "stats_hourly", "stats_model",
			"stats_channel", "stats_api_key", "stats_daily_channel",
			"stats_daily_model", "stats_daily_api_key", "stats_daily_channel_model",
		)
	}
	if d.IncludeLogs {
		names = append(names,
			"error_logs", "audit_logs", "relay_logs", "relay_log_attempts",
			"relay_log_content_refs", "relay_log_content_blobs",
		)
	}
	return names
}

// authoritativeSections builds the v6 authority set and validates that the
// manifest is present. A v6 dump without an explicit manifest is rejected
// instead of guessing which collections may be cleared.
func authoritativeSections(dump *model.DBDump) (map[string]bool, error) {
	if dump.Version < 6 {
		return nil, nil
	}
	if len(dump.Sections) == 0 {
		return nil, fmt.Errorf("backup version 6 is missing the sections manifest")
	}
	known := make(map[string]bool)
	for _, s := range allSections() {
		known[s.name] = true
	}
	for _, name := range []string{
		"stats_total", "stats_daily", "stats_hourly", "stats_model",
		"stats_channel", "stats_api_key", "stats_daily_channel",
		"stats_daily_model", "stats_daily_api_key", "stats_daily_channel_model",
		"error_logs", "audit_logs", "relay_logs", "relay_log_attempts",
		"relay_log_content_refs", "relay_log_content_blobs",
	} {
		known[name] = true
	}
	authority := make(map[string]bool, len(dump.Sections))
	for _, name := range dump.Sections {
		if !known[name] {
			return nil, fmt.Errorf("backup version 6 declares unknown section %q", name)
		}
		authority[name] = true
	}
	return authority, nil
}

// channelIDMapping resolves source channel IDs to target IDs. v6 incremental
// imports match by channel_uid first; a stable UID that already lives under a
// different target numeric ID is remapped, and every dependent row follows the
// mapping. Legacy dumps (v1-v5) keep the historical numeric-ID behavior.
func channelIDMapping(ctx context.Context, target *gorm.DB, dump *model.DBDump, isFull bool, authority map[string]bool, explicit map[int]int, missingUID map[int]bool) (map[int]int, []model.DBChannelMapping, error) {
	mapping := make(map[int]int, len(dump.Channels))
	if dump.Version < 6 {
		// v1-v5 keep the historical numeric-ID semantics and rely on the
		// existing channel_uid conflict guard instead of UID-first remapping.
		for _, channel := range dump.Channels {
			mapping[channel.ID] = channel.ID
		}
		return mapping, nil, nil
	}
	if isFull && authority["channels"] {
		// A full restore rebuilds the channel table from source rows, so source
		// IDs are authoritative and no remapping is needed.
		for _, channel := range dump.Channels {
			mapping[channel.ID] = channel.ID
		}
		return mapping, nil, nil
	}

	var targetChannels []model.Channel
	if err := target.WithContext(ctx).Select("id", "channel_uid", "name").Find(&targetChannels).Error; err != nil {
		return nil, nil, fmt.Errorf("resolve channel identities: %w", err)
	}
	byUID := make(map[string]model.Channel, len(targetChannels))
	byID := make(map[int]model.Channel, len(targetChannels))
	for _, channel := range targetChannels {
		if channel.ChannelUID != "" {
			byUID[channel.ChannelUID] = channel
		}
		byID[channel.ID] = channel
	}

	var mappings []model.DBChannelMapping
	claimed := make(map[int]bool, len(dump.Channels))
	for _, source := range dump.Channels {
		if targetID, ok := explicit[source.ID]; ok {
			hit, exists := byID[targetID]
			if !exists {
				return nil, nil, fmt.Errorf("explicit channel mapping %d -> %d points to a missing target channel", source.ID, targetID)
			}
			if source.ChannelUID != "" && hit.ChannelUID != "" && hit.ChannelUID != source.ChannelUID {
				return nil, nil, fmt.Errorf("explicit channel mapping %d -> %d conflicts with target channel_uid", source.ID, targetID)
			}
			mapping[source.ID] = targetID
			claimed[targetID] = true
			mappings = append(mappings, model.DBChannelMapping{
				SourceChannelID: source.ID,
				TargetChannelID: targetID,
				ChannelUID:      source.ChannelUID,
				Reason:          "explicit",
			})
			continue
		}
		if source.ChannelUID != "" {
			if hit, ok := byUID[source.ChannelUID]; ok {
				mapping[source.ID] = hit.ID
				if hit.ID != source.ID {
					mappings = append(mappings, model.DBChannelMapping{
						SourceChannelID: source.ID,
						TargetChannelID: hit.ID,
						ChannelUID:      source.ChannelUID,
						Reason:          "channel_uid",
					})
				}
				claimed[hit.ID] = true
				continue
			}
		}
		// No UID match. If the source numeric ID is free (or belongs to the same
		// UID-less legacy row), keep it. Otherwise report the collision so the
		// administrator can supply an explicit mapping.
		if hit, occupied := byID[source.ID]; occupied && missingUID[source.ID] && hit.ChannelUID != "" {
			// A legacy dump has no stable identity for this row. Reusing a target
			// row that already has a UID would claim an identity from an older
			// backup. Fail and ask for an explicit mapping instead.
			return nil, nil, fmt.Errorf(
				"channel id collision: legacy backup row %d (%q) has no channel_uid but target id %d is owned by %q; supply an explicit source-to-target channel mapping",
				source.ID, source.Name, hit.ID, hit.Name,
			)
		}
		if hit, occupied := byID[source.ID]; occupied && hit.ChannelUID != "" && source.ChannelUID != "" && hit.ChannelUID != source.ChannelUID {
			return nil, nil, fmt.Errorf(
				"channel id collision: source channel %d (%q) cannot use target id %d owned by %q; supply an explicit source-to-target channel mapping",
				source.ID, source.Name, hit.ID, hit.Name,
			)
		}
		if claimed[source.ID] {
			return nil, nil, fmt.Errorf("channel id collision inside backup: multiple channels resolve to target id %d", source.ID)
		}
		mapping[source.ID] = source.ID
		claimed[source.ID] = true
	}
	for _, id := range referencedChannelIDs(dump) {
		if _, resolved := mapping[id]; resolved {
			continue
		}
		if _, exists := byID[id]; !exists {
			return nil, nil, fmt.Errorf("backup references channel id %d that exists in neither the backup nor the target", id)
		}
		mapping[id] = id
	}
	return mapping, mappings, nil
}

func referencedChannelIDs(dump *model.DBDump) []int {
	seen := make(map[int]bool)
	ids := make([]int, 0)
	add := func(id int) {
		if id <= 0 || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, row := range dump.ChannelKeys {
		add(row.ChannelID)
	}
	for _, row := range dump.GroupItems {
		add(row.ChannelID)
	}
	for _, row := range dump.ChannelModelLimits {
		add(row.ChannelID)
	}
	for _, row := range dump.ChannelProbeRuns {
		add(row.ChannelID)
	}
	for _, row := range dump.ChannelProbeResults {
		add(row.ChannelID)
	}
	for _, row := range dump.ChannelModelCapabilities {
		add(row.ChannelID)
	}
	return ids
}

// rewriteChannelForeignKeys applies the resolved mapping to every collection
// that stores a channel ID, so a remapped channel can never receive another
// channel's group items, keys or probe evidence.
func rewriteChannelForeignKeys(dump *model.DBDump, mapping map[int]int) error {
	remap := func(id int) (int, error) {
		target, ok := mapping[id]
		if !ok {
			return 0, fmt.Errorf("backup references unknown source channel id %d", id)
		}
		return target, nil
	}
	for i := range dump.ChannelKeys {
		id, err := remap(dump.ChannelKeys[i].ChannelID)
		if err != nil {
			return err
		}
		dump.ChannelKeys[i].ChannelID = id
	}
	for i := range dump.GroupItems {
		id, err := remap(dump.GroupItems[i].ChannelID)
		if err != nil {
			return err
		}
		dump.GroupItems[i].ChannelID = id
	}
	for i := range dump.ChannelModelLimits {
		id, err := remap(dump.ChannelModelLimits[i].ChannelID)
		if err != nil {
			return err
		}
		dump.ChannelModelLimits[i].ChannelID = id
	}
	for i := range dump.ChannelProbeRuns {
		id, err := remap(dump.ChannelProbeRuns[i].ChannelID)
		if err != nil {
			return err
		}
		dump.ChannelProbeRuns[i].ChannelID = id
	}
	for i := range dump.ChannelProbeResults {
		id, err := remap(dump.ChannelProbeResults[i].ChannelID)
		if err != nil {
			return err
		}
		dump.ChannelProbeResults[i].ChannelID = id
	}
	for i := range dump.ChannelModelCapabilities {
		id, err := remap(dump.ChannelModelCapabilities[i].ChannelID)
		if err != nil {
			return err
		}
		dump.ChannelModelCapabilities[i].ChannelID = id
	}
	return nil
}

// insertSectionExact upserts one v6 section by its natural key using exact
// values, including explicit false, zero and empty strings.
func (c *importConfig) insertSectionExact(s section, rows any, count int) error {
	if count == 0 {
		return nil
	}
	conflict := clause.OnConflict{DoNothing: true}
	if len(s.conflict) > 0 {
		conflict = clause.OnConflict{Columns: s.conflict, UpdateAll: true}
	} else if c.isFull {
		conflict = clause.OnConflict{DoNothing: true}
	} else {
		conflict = clause.OnConflict{Columns: primaryKeyColumns(s.model, c.conn), UpdateAll: true}
	}
	return c.insertExactValues(s.table, rows, conflict)
}

func primaryKeyColumns(modelValue any, conn *gorm.DB) []clause.Column {
	parsed, err := schema.Parse(modelValue, &batchSchemaCache, conn.NamingStrategy)
	if err != nil || len(parsed.PrimaryFields) == 0 {
		return nil
	}
	columns := make([]clause.Column, 0, len(parsed.PrimaryFields))
	for _, field := range parsed.PrimaryFields {
		columns = append(columns, clause.Column{Name: field.DBName})
	}
	return columns
}
