package backup

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"reflect"
	"sort"
	"strconv"
	"strings"

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

	hasChannels := sectionHasRows(dump, authority, "channels")
	sourceChannels := []model.Channel(nil)
	if hasChannels {
		sourceChannels = dump.Channels
	}
	if isFull && hasChannels {
		for _, channel := range sourceChannels {
			mapping[channel.ID] = channel.ID
		}
		requiredReferences := requiredReferencedChannelIDs(dump, authority)
		for _, id := range referencedChannelIDs(dump, authority) {
			if _, resolved := mapping[id]; resolved {
				continue
			}
			if requiredReferences[id] {
				return nil, nil, fmt.Errorf("full restore references channel id %d absent from its authoritative channels section", id)
			}
			mapping[id] = id
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
	claimed := make(map[int]bool, len(sourceChannels))
	for _, source := range sourceChannels {
		if targetID, ok := explicit[source.ID]; ok {
			hit, exists := byID[targetID]
			if !exists {
				return nil, nil, fmt.Errorf("explicit channel mapping %d -> %d points to a missing target channel", source.ID, targetID)
			}
			if source.ChannelUID != "" && hit.ChannelUID != "" && hit.ChannelUID != source.ChannelUID {
				return nil, nil, fmt.Errorf("explicit channel mapping %d -> %d conflicts with target channel_uid", source.ID, targetID)
			}
			if claimed[targetID] {
				return nil, nil, fmt.Errorf("channel id collision inside backup: multiple channels resolve to target id %d", targetID)
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
				if claimed[hit.ID] {
					return nil, nil, fmt.Errorf("channel id collision inside backup: multiple channels resolve to target id %d", hit.ID)
				}
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
	requiredReferences := requiredReferencedChannelIDs(dump, authority)
	for _, id := range referencedChannelIDs(dump, authority) {
		if _, resolved := mapping[id]; resolved {
			continue
		}
		targetID := id
		reason := "numeric_id"
		if explicitID, ok := explicit[id]; ok {
			targetID = explicitID
			reason = "explicit"
		}
		if _, exists := byID[targetID]; !exists {
			if requiredReferences[id] {
				return nil, nil, fmt.Errorf("backup references channel id %d that exists in neither the backup nor the target", id)
			}
			mapping[id] = id
			continue
		}
		if claimed[targetID] {
			return nil, nil, fmt.Errorf("channel id collision inside backup: multiple source references resolve to target id %d", targetID)
		}
		mapping[id] = targetID
		claimed[targetID] = true
		if targetID != id {
			mappings = append(mappings, model.DBChannelMapping{
				SourceChannelID: id,
				TargetChannelID: targetID,
				Reason:          reason,
			})
		}
	}
	return mapping, mappings, nil
}

func requiredReferencedChannelIDs(dump *model.DBDump, authority map[string]bool) map[int]bool {
	required := make(map[int]bool)
	add := func(id int) {
		if id > 0 {
			required[id] = true
		}
	}
	if sectionHasRows(dump, authority, "channel_keys") {
		for _, row := range dump.ChannelKeys {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "group_items") {
		for _, row := range dump.GroupItems {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "channel_model_limits") {
		for _, row := range dump.ChannelModelLimits {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "channel_probe_runs") {
		for _, row := range dump.ChannelProbeRuns {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "channel_probe_results") {
		for _, row := range dump.ChannelProbeResults {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "channel_model_capabilities") {
		for _, row := range dump.ChannelModelCapabilities {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "runtime_states") {
		for index, row := range dump.RuntimeStates {
			if rowHasNonNullField(dump, "runtime_states", index, "channel_id") {
				add(row.ChannelID)
			}
			if sourceID, ok := derivedChannelKeySourceID(row.Key); ok {
				add(sourceID)
			}
		}
	}
	if sectionHasRows(dump, authority, "circuit_breaker_states") {
		for index, row := range dump.CircuitBreakerStates {
			if rowHasNonNullField(dump, "circuit_breaker_states", index, "channel_id") {
				add(row.ChannelID)
			}
			if sourceID, ok := derivedChannelKeySourceID(row.Key); ok {
				add(sourceID)
			}
		}
	}
	return required
}

func sectionHasRows(dump *model.DBDump, authority map[string]bool, name string) bool {
	if dump == nil {
		return false
	}
	if dump.Version < 6 {
		return true
	}
	if !authority[name] {
		return false
	}
	rows, ok := sectionRowsByName(dump, name)
	return ok && !isNullSection(rows)
}

func referencedChannelIDs(dump *model.DBDump, authority map[string]bool) []int {
	seen := make(map[int]bool)
	ids := make([]int, 0)
	add := func(id int) {
		if id <= 0 || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if sectionHasRows(dump, authority, "channel_keys") {
		for _, row := range dump.ChannelKeys {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "group_items") {
		for _, row := range dump.GroupItems {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "channel_model_limits") {
		for _, row := range dump.ChannelModelLimits {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "channel_probe_runs") {
		for _, row := range dump.ChannelProbeRuns {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "channel_probe_results") {
		for _, row := range dump.ChannelProbeResults {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "channel_model_capabilities") {
		for _, row := range dump.ChannelModelCapabilities {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "stats_channel") {
		for _, row := range dump.StatsChannel {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "stats_model") {
		for _, row := range dump.StatsModel {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "stats_daily_channel") {
		for _, row := range dump.StatsDailyChannel {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "stats_daily_channel_model") {
		for _, row := range dump.StatsDailyChannelModel {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "runtime_states") {
		for index, row := range dump.RuntimeStates {
			if rowHasNonNullField(dump, "runtime_states", index, "channel_id") {
				add(row.ChannelID)
			}
			if sourceID, ok := derivedChannelKeySourceID(row.Key); ok {
				add(sourceID)
			}
		}
	}
	if sectionHasRows(dump, authority, "circuit_breaker_states") {
		for index, row := range dump.CircuitBreakerStates {
			if rowHasNonNullField(dump, "circuit_breaker_states", index, "channel_id") {
				add(row.ChannelID)
			}
			if sourceID, ok := derivedChannelKeySourceID(row.Key); ok {
				add(sourceID)
			}
		}
	}
	if sectionHasRows(dump, authority, "group_health_attempts") {
		for _, row := range dump.GroupHealthAttempts {
			add(row.ChannelID)
		}
	}
	if sectionHasRows(dump, authority, "group_health_snapshots") {
		for _, row := range dump.GroupHealthSnapshots {
			if row.SuccessfulChannelID != nil {
				add(*row.SuccessfulChannelID)
			}
		}
	}
	if sectionHasRows(dump, authority, "ai_route_tasks") {
		for _, task := range dump.AIRouteTasks {
			for _, channel := range task.Channels {
				add(channel.ChannelID)
			}
			if task.CurrentBatch != nil {
				for _, id := range task.CurrentBatch.ChannelIDs {
					add(id)
				}
			}
			for _, batch := range task.RunningBatches {
				for _, id := range batch.ChannelIDs {
					add(id)
				}
			}
		}
	}
	if sectionHasRows(dump, authority, "relay_logs") {
		for _, row := range dump.RelayLogs {
			add(row.ChannelId)
			for _, attempt := range row.Attempts {
				add(attempt.ChannelID)
			}
		}
	}
	if sectionHasRows(dump, authority, "relay_log_attempts") {
		for _, row := range dump.RelayLogAttempts {
			add(row.ChannelID)
		}
	}
	return ids
}

// rewriteChannelForeignKeys applies identity remapping only to rows explicitly
// owned by this v6 manifest. Unlisted and null sections remain untouched.
func rewriteChannelForeignKeys(dump *model.DBDump, mapping map[int]int, authority map[string]bool) error {
	remap := func(id int) (int, error) {
		target, ok := mapping[id]
		if !ok {
			return 0, fmt.Errorf("backup references unknown source channel id %d", id)
		}
		return target, nil
	}
	if sectionHasRows(dump, authority, "channels") {
		for i := range dump.Channels {
			id, err := remap(dump.Channels[i].ID)
			if err != nil {
				return err
			}
			dump.Channels[i].ID = id
		}
	}
	if sectionHasRows(dump, authority, "channel_keys") {
		for i := range dump.ChannelKeys {
			if !rowHasNonNullField(dump, "channel_keys", i, "channel_id") {
				continue
			}
			id, err := remap(dump.ChannelKeys[i].ChannelID)
			if err != nil {
				return err
			}
			dump.ChannelKeys[i].ChannelID = id
		}
	}
	if sectionHasRows(dump, authority, "group_items") {
		for i := range dump.GroupItems {
			if !rowHasNonNullField(dump, "group_items", i, "channel_id") {
				continue
			}
			id, err := remap(dump.GroupItems[i].ChannelID)
			if err != nil {
				return err
			}
			dump.GroupItems[i].ChannelID = id
		}
	}
	if sectionHasRows(dump, authority, "channel_model_limits") {
		for i := range dump.ChannelModelLimits {
			if !rowHasNonNullField(dump, "channel_model_limits", i, "channel_id") {
				continue
			}
			id, err := remap(dump.ChannelModelLimits[i].ChannelID)
			if err != nil {
				return err
			}
			dump.ChannelModelLimits[i].ChannelID = id
		}
	}
	if sectionHasRows(dump, authority, "channel_probe_runs") {
		for i := range dump.ChannelProbeRuns {
			if !rowHasNonNullField(dump, "channel_probe_runs", i, "channel_id") {
				continue
			}
			id, err := remap(dump.ChannelProbeRuns[i].ChannelID)
			if err != nil {
				return err
			}
			dump.ChannelProbeRuns[i].ChannelID = id
		}
	}
	if sectionHasRows(dump, authority, "channel_probe_results") {
		for i := range dump.ChannelProbeResults {
			if !rowHasNonNullField(dump, "channel_probe_results", i, "channel_id") {
				continue
			}
			id, err := remap(dump.ChannelProbeResults[i].ChannelID)
			if err != nil {
				return err
			}
			dump.ChannelProbeResults[i].ChannelID = id
		}
	}
	if sectionHasRows(dump, authority, "channel_model_capabilities") {
		for i := range dump.ChannelModelCapabilities {
			if !rowHasNonNullField(dump, "channel_model_capabilities", i, "channel_id") {
				continue
			}
			oldID := dump.ChannelModelCapabilities[i].ChannelID
			id, err := remap(dump.ChannelModelCapabilities[i].ChannelID)
			if err != nil {
				return err
			}
			dump.ChannelModelCapabilities[i].ChannelID = id
			if id != oldID && model.IsLegacyCapabilityScopeUID(dump.ChannelModelCapabilities[i].ChannelUID) {
				dump.ChannelModelCapabilities[i].ChannelUID = model.CapabilityScopeUID("", id)
			}
		}
	}
	if sectionHasRows(dump, authority, "stats_channel") {
		for i := range dump.StatsChannel {
			if !rowHasNonNullField(dump, "stats_channel", i, "channel_id") {
				continue
			}
			id, err := remap(dump.StatsChannel[i].ChannelID)
			if err != nil {
				return err
			}
			dump.StatsChannel[i].ChannelID = id
		}
	}
	if sectionHasRows(dump, authority, "stats_model") {
		for i := range dump.StatsModel {
			if !rowHasNonNullField(dump, "stats_model", i, "channel_id") {
				if mappedStatsModelIDs(mapping) {
					return fmt.Errorf("stats_model row %d: channel_id is required to map a model statistic across channel identities", i)
				}
				continue
			}
			oldID := dump.StatsModel[i].ChannelID
			if oldID == 0 {
				continue
			}
			id, err := remap(oldID)
			if err != nil {
				return err
			}
			dump.StatsModel[i].ChannelID = id
			if id != oldID {
				if !rowHasNonNullField(dump, "stats_model", i, "name") ||
					strings.TrimSpace(dump.StatsModel[i].Name) == "" {
					return fmt.Errorf("stats_model row %d: non-empty name is required to remap its derived ID from channel %d to %d", i, oldID, id)
				}
				dump.StatsModel[i].ID = statsModelID(id, dump.StatsModel[i].Name)
			}
		}
	}
	if sectionHasRows(dump, authority, "stats_daily_channel") {
		for i := range dump.StatsDailyChannel {
			if !rowHasNonNullField(dump, "stats_daily_channel", i, "channel_id") {
				continue
			}
			id, err := remap(dump.StatsDailyChannel[i].ChannelID)
			if err != nil {
				return err
			}
			dump.StatsDailyChannel[i].ChannelID = id
		}
	}
	if sectionHasRows(dump, authority, "stats_daily_channel_model") {
		for i := range dump.StatsDailyChannelModel {
			if !rowHasNonNullField(dump, "stats_daily_channel_model", i, "channel_id") {
				continue
			}
			id, err := remap(dump.StatsDailyChannelModel[i].ChannelID)
			if err != nil {
				return err
			}
			dump.StatsDailyChannelModel[i].ChannelID = id
		}
	}
	if sectionHasRows(dump, authority, "runtime_states") {
		for i := range dump.RuntimeStates {
			oldID := dump.RuntimeStates[i].ChannelID
			if !rowHasNonNullField(dump, "runtime_states", i, "channel_id") {
				sourceID, ok := derivedChannelKeySourceID(dump.RuntimeStates[i].Key)
				if !ok {
					return fmt.Errorf("runtime_states row %d: channel_id is omitted or null and key %q does not identify a channel", i, dump.RuntimeStates[i].Key)
				}
				oldID = sourceID
			}
			id, err := remap(oldID)
			if err != nil {
				return fmt.Errorf("runtime_states row %d: %w", i, err)
			}
			if rowHasNonNullField(dump, "runtime_states", i, "channel_id") {
				dump.RuntimeStates[i].ChannelID = id
			}
			if id != oldID {
				key, ok := remapDerivedChannelKey(dump.RuntimeStates[i].Key, oldID, id)
				if !ok {
					return fmt.Errorf("runtime_states row %d: key %q does not start with source channel id %d", i, dump.RuntimeStates[i].Key, oldID)
				}
				dump.RuntimeStates[i].Key = key
			}
		}
	}
	if sectionHasRows(dump, authority, "circuit_breaker_states") {
		for i := range dump.CircuitBreakerStates {
			oldID := dump.CircuitBreakerStates[i].ChannelID
			if !rowHasNonNullField(dump, "circuit_breaker_states", i, "channel_id") {
				sourceID, ok := derivedChannelKeySourceID(dump.CircuitBreakerStates[i].Key)
				if !ok {
					return fmt.Errorf("circuit_breaker_states row %d: channel_id is omitted or null and key %q does not identify a channel", i, dump.CircuitBreakerStates[i].Key)
				}
				oldID = sourceID
			}
			id, err := remap(oldID)
			if err != nil {
				return fmt.Errorf("circuit_breaker_states row %d: %w", i, err)
			}
			if rowHasNonNullField(dump, "circuit_breaker_states", i, "channel_id") {
				dump.CircuitBreakerStates[i].ChannelID = id
			}
			if id != oldID {
				key, ok := remapDerivedChannelKey(dump.CircuitBreakerStates[i].Key, oldID, id)
				if !ok {
					return fmt.Errorf("circuit_breaker_states row %d: key %q does not start with source channel id %d", i, dump.CircuitBreakerStates[i].Key, oldID)
				}
				dump.CircuitBreakerStates[i].Key = key
			}
		}
	}
	if sectionHasRows(dump, authority, "group_health_attempts") {
		for i := range dump.GroupHealthAttempts {
			if !rowHasNonNullField(dump, "group_health_attempts", i, "channel_id") {
				continue
			}
			id, err := remap(dump.GroupHealthAttempts[i].ChannelID)
			if err != nil {
				return err
			}
			dump.GroupHealthAttempts[i].ChannelID = id
		}
	}
	if sectionHasRows(dump, authority, "group_health_snapshots") {
		for i := range dump.GroupHealthSnapshots {
			if !rowHasNonNullField(dump, "group_health_snapshots", i, "successful_channel_id") {
				continue
			}
			channelID := dump.GroupHealthSnapshots[i].SuccessfulChannelID
			if channelID == nil {
				continue
			}
			id, err := remap(*channelID)
			if err != nil {
				return err
			}
			dump.GroupHealthSnapshots[i].SuccessfulChannelID = &id
		}
	}
	if sectionHasRows(dump, authority, "ai_route_tasks") {
		for i := range dump.AIRouteTasks {
			task := &dump.AIRouteTasks[i]
			for j := range task.Channels {
				id, err := remap(task.Channels[j].ChannelID)
				if err != nil {
					return err
				}
				task.Channels[j].ChannelID = id
			}
			if task.CurrentBatch != nil {
				if err := remapChannelIDSlice(task.CurrentBatch.ChannelIDs, remap); err != nil {
					return err
				}
			}
			for j := range task.RunningBatches {
				if err := remapChannelIDSlice(task.RunningBatches[j].ChannelIDs, remap); err != nil {
					return err
				}
			}
		}
	}
	if sectionHasRows(dump, authority, "relay_logs") {
		for i := range dump.RelayLogs {
			if dump.RelayLogs[i].ChannelId != 0 {
				id, err := remap(dump.RelayLogs[i].ChannelId)
				if err != nil {
					return fmt.Errorf("relay_logs row %d: %w", i, err)
				}
				dump.RelayLogs[i].ChannelId = id
			}
			for j := range dump.RelayLogs[i].Attempts {
				if dump.RelayLogs[i].Attempts[j].ChannelID == 0 {
					continue
				}
				attemptID, err := remap(dump.RelayLogs[i].Attempts[j].ChannelID)
				if err != nil {
					return fmt.Errorf("relay_logs row %d attempt %d: %w", i, j, err)
				}
				dump.RelayLogs[i].Attempts[j].ChannelID = attemptID
			}
		}
	}
	if sectionHasRows(dump, authority, "relay_log_attempts") {
		for i := range dump.RelayLogAttempts {
			if dump.RelayLogAttempts[i].ChannelID == 0 {
				continue
			}
			id, err := remap(dump.RelayLogAttempts[i].ChannelID)
			if err != nil {
				return fmt.Errorf("relay_log_attempts row %d: %w", i, err)
			}
			dump.RelayLogAttempts[i].ChannelID = id
		}
	}
	return nil
}

func rowHasNonNullField(dump *model.DBDump, section string, index int, field string) bool {
	if dump == nil || dump.Version < 6 {
		return true
	}
	if section == "relay_log_attempts" && field == "channel_id" {
		if nonNull, tracked := dump.RelayLogAttemptChannelIDNonNull(index); tracked {
			return nonNull
		}
		return true
	}
	presence, ok := dump.RowFieldPresence(section, index)
	if !ok {
		return true
	}
	return presence.NonNull[field]
}

func mappedStatsModelIDs(mapping map[int]int) bool {
	for sourceID, targetID := range mapping {
		if sourceID != targetID {
			return true
		}
	}
	return false
}

func remapDerivedChannelKey(key string, sourceID, targetID int) (string, bool) {
	prefix := fmt.Sprintf("%d:", sourceID)
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	return fmt.Sprintf("%d:%s", targetID, strings.TrimPrefix(key, prefix)), true
}

func derivedChannelKeySourceID(key string) (int, bool) {
	prefix, _, ok := strings.Cut(key, ":")
	if !ok {
		return 0, false
	}
	sourceID, err := strconv.Atoi(prefix)
	if err != nil || sourceID <= 0 {
		return 0, false
	}
	return sourceID, true
}

func remapChannelIDSlice(ids []int, remap func(int) (int, error)) error {
	for i := range ids {
		id, err := remap(ids[i])
		if err != nil {
			return err
		}
		ids[i] = id
	}
	return nil
}

func statsModelID(channelID int, modelName string) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(fmt.Sprintf("%d:%s", channelID, strings.ToLower(strings.TrimSpace(modelName)))))
	return int64(hash.Sum64() & 0x7fffffffffffffff)
}

// insertSectionExact upserts one v6 section by its natural key using exact
// values, including explicit false, zero and empty strings. Incremental imports
// decoded from JSON are field patches: omitted and explicit-null fields keep
// the target value instead of being replaced by Go zero values.
func (c *importConfig) insertSectionExact(dump *model.DBDump, s section, rows any, count int) error {
	return c.insertSectionExactWithIndexes(dump, s, rows, count, nil)
}

// insertSectionExactWithIndexes is the same operation when the importer has
// filtered rows before writing (for example, deployment-owned settings). The
// indexes connect each retained row back to its original JSON field-presence
// metadata.
func (c *importConfig) insertSectionExactWithIndexes(dump *model.DBDump, s section, rows any, count int, sourceIndexes []int) error {
	if count == 0 {
		return nil
	}
	if !c.isFull && dump != nil {
		firstSourceIndex := 0
		if len(sourceIndexes) > 0 {
			firstSourceIndex = sourceIndexes[0]
		}
		if _, hasPresence := dump.RowFieldPresence(s.name, firstSourceIndex); hasPresence {
			return c.patchSectionExact(dump, s, rows, sourceIndexes)
		}
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

// patchSectionExact applies only fields that appeared with a non-null value in
// each decoded JSON row. The natural/primary key must therefore be present; it
// identifies the target row, while no other omitted field is allowed to become
// a zero-value overwrite.
func (c *importConfig) patchSectionExact(dump *model.DBDump, s section, rows any, sourceIndexes []int) error {
	slice := reflect.ValueOf(rows)
	if slice.Kind() != reflect.Pointer || slice.IsNil() || slice.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("%s: rows must be *[]T, got %T", s.table, rows)
	}
	slice = slice.Elem()
	if sourceIndexes != nil && len(sourceIndexes) != slice.Len() {
		return fmt.Errorf("%s: row/source index count mismatch: %d rows, %d indexes", s.name, slice.Len(), len(sourceIndexes))
	}

	conflictColumns := s.conflict
	if len(conflictColumns) == 0 {
		conflictColumns = primaryKeyColumns(s.model, c.conn)
	}
	if len(conflictColumns) == 0 {
		return fmt.Errorf("%s: incremental patch requires a primary or natural key", s.table)
	}

	modelValue := reflect.New(slice.Type().Elem()).Interface()
	modelSchema, err := schema.Parse(modelValue, &batchSchemaCache, c.conn.NamingStrategy)
	if err != nil {
		return fmt.Errorf("%s: parse schema for incremental patch: %w", s.table, err)
	}
	keyJSONFields := make([]string, 0, len(conflictColumns))
	for _, column := range conflictColumns {
		jsonField := jsonFieldNameForDBColumn(modelSchema, column.Name)
		if jsonField == "" {
			return fmt.Errorf("%s: cannot resolve JSON field for key column %q", s.table, column.Name)
		}
		keyJSONFields = append(keyJSONFields, jsonField)
	}

	var totalAffected int64
	for rowIndex := 0; rowIndex < slice.Len(); rowIndex++ {
		sourceIndex := rowIndex
		if sourceIndexes != nil {
			sourceIndex = sourceIndexes[rowIndex]
		}
		presence, ok := dump.RowFieldPresence(s.name, sourceIndex)
		if !ok {
			return fmt.Errorf("%s: missing field-presence metadata for row %d", s.name, sourceIndex)
		}
		for _, keyField := range keyJSONFields {
			if !presence.NonNull[keyField] {
				return fmt.Errorf(
					"%s row %d: incremental patch requires non-null key field %q",
					s.name, sourceIndex, keyField,
				)
			}
		}

		values := make(map[string]any, len(modelSchema.DBNames))
		updateColumns := make([]string, 0, len(modelSchema.DBNames))
		where := make(map[string]any, len(conflictColumns))
		for _, dbName := range modelSchema.DBNames {
			field := modelSchema.FieldsByDBName[dbName]
			jsonField := jsonTagName(field)
			if !shouldPatchDatabaseField(s.name, dbName, jsonField, presence) {
				continue
			}
			value, _ := field.ValueOf(c.conn.Statement.Context, slice.Index(rowIndex))
			values[dbName] = value
			updateColumns = append(updateColumns, dbName)
		}
		sort.Strings(updateColumns)
		for _, column := range conflictColumns {
			where[column.Name] = values[column.Name]
		}
		if s.name == "channels" && presence.NonNull["channel_uid"] {
			var targetChannel model.Channel
			err := c.conn.Select("channel_uid").Where("id = ?", values["id"]).First(&targetChannel).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%s row %d: read target channel identity: %w", s.name, sourceIndex, err)
			}
			if err == nil && targetChannel.ChannelUID != values["channel_uid"] {
				return fmt.Errorf("%s row %d: channel_uid is immutable for an existing channel", s.name, sourceIndex)
			}
		}
		var existing int64
		if err := c.conn.Model(modelValue).Table(s.table).Where(where).Count(&existing).Error; err != nil {
			appendStep(c.res, s.table, "patch", totalAffected, err)
			return fmt.Errorf("%s row %d: locate target for incremental patch: %w", s.name, sourceIndex, err)
		}
		var result *gorm.DB
		if existing > 0 {
			updateValues := make(map[string]any, len(updateColumns))
			for _, column := range updateColumns {
				if _, isKey := where[column]; isKey || (s.name == "channels" && column == "channel_uid") {
					continue
				}
				updateValues[column] = values[column]
			}
			if len(updateValues) == 0 {
				totalAffected++
				continue
			}
			result = c.conn.Model(modelValue).Table(s.table).Where(where).Updates(updateValues)
		} else {
			// Sparse channel inserts bypass GORM create hooks, so populate the
			// immutable UID before writing the map of supplied patch fields.
			if s.name == "channels" {
				channel := slice.Index(rowIndex).Addr().Interface().(*model.Channel)
				channel.EnsureChannelUID()
				field := modelSchema.FieldsByDBName["channel_uid"]
				values["channel_uid"], _ = field.ValueOf(c.conn.Statement.Context, slice.Index(rowIndex))
			}
			result = c.conn.Model(modelValue).Table(s.table).Create(&values)
		}
		if result.Error != nil {
			appendStep(c.res, s.table, "patch", totalAffected, result.Error)
			return fmt.Errorf("%s row %d: incremental patch: %w", s.name, sourceIndex, result.Error)
		}
		totalAffected += result.RowsAffected
	}
	appendStep(c.res, s.table, "patch", totalAffected, nil)
	return nil
}

func shouldPatchDatabaseField(sectionName, dbName, jsonField string, presence model.DBDumpRowFieldPresence) bool {
	if jsonField != "" && presence.NonNull[jsonField] {
		return true
	}
	// api_key_hash is intentionally json:"-" but is a deterministic derivative of
	// api_key. Recompute and persist it when the source field is explicitly
	// patched, without exposing the hash in backup files.
	return sectionName == "api_keys" && dbName == "api_key_hash" && presence.NonNull["api_key"]
}

func jsonFieldNameForDBColumn(modelSchema *schema.Schema, column string) string {
	if modelSchema == nil {
		return ""
	}
	return jsonTagName(modelSchema.FieldsByDBName[column])
}

func jsonTagName(field *schema.Field) string {
	if field == nil {
		return ""
	}
	name, tagged := field.StructField.Tag.Lookup("json")
	if !tagged {
		return field.StructField.Name
	}
	if comma := strings.IndexByte(name, ','); comma >= 0 {
		name = name[:comma]
	}
	if name == "-" {
		return ""
	}
	if name == "" {
		return field.StructField.Name
	}
	return name
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
