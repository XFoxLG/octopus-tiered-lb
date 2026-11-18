package backup

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
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
func importV6Sections(cfg *importConfig, dump *model.DBDump, authority map[string]bool, skipRelayLogs bool) (returnErr error) {
	if cfg.isFull {
		logConn := cfg.conn
		openedLogDBForPreflight := false
		if skipRelayLogs {
			logConn = db.GetLogDB()
			if logConn == nil {
				needsLogDB, err := v6PreflightNeedsLogDBLookup(cfg.conn, dump, authority)
				if err != nil {
					return err
				}
				if needsLogDB {
					if err := db.ReopenLogDB(); err != nil {
						return fmt.Errorf("reopen separate log database for v6 restore preflight: %w", err)
					}
					logConn = db.GetLogDB()
					if logConn == nil {
						return fmt.Errorf("separate log database remained unavailable after reopening for v6 restore preflight")
					}
					openedLogDBForPreflight = true
				}
			}
		}
		if openedLogDBForPreflight {
			defer func() {
				if err := db.CloseLogDB(); err != nil && returnErr == nil {
					returnErr = fmt.Errorf("close separate log database after v6 restore preflight: %w", err)
				}
			}()
		}
		if err := validateChannelSectionDependencies(cfg.conn, dump, authority, logConn); err != nil {
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

// validateChannelSectionDependencies checks references before a full restore
// deletes anything. Channel IDs that change identity cannot be left in target
// sections the backup does not own.
func validateChannelSectionDependencies(conn *gorm.DB, dump *model.DBDump, authority map[string]bool, logConn *gorm.DB) error {
	if conn == nil || dump == nil {
		return fmt.Errorf("full restore dependency check requires a database and backup")
	}
	replacesChannels := sectionHasRows(dump, authority, "channels")
	if replacesChannels {
		for _, dependent := range []string{"channel_keys", "group_items"} {
			if !sectionHasRows(dump, authority, dependent) {
				return fmt.Errorf(
					"full restore replaces channels but does not own %q; include that section in the backup manifest or omit channels",
					dependent,
				)
			}
		}
		changedIDs, err := changedChannelIDs(conn, dump)
		if err != nil {
			return err
		}
		if len(changedIDs) > 0 {
			if err := validateUnownedChannelReferences(conn, dump, authority, changedIDs, logConn); err != nil {
				return err
			}
		}
	}

	groupsImported := sectionHasRows(dump, authority, "groups")
	itemsImported := sectionHasRows(dump, authority, "group_items")
	switch {
	case itemsImported && groupsImported:
		sourceGroups := make(map[int]bool, len(dump.Groups))
		for _, group := range dump.Groups {
			sourceGroups[group.ID] = true
		}
		for _, item := range dump.GroupItems {
			if !sourceGroups[item.GroupID] {
				return fmt.Errorf("full restore group_items references group id %d absent from the imported groups section", item.GroupID)
			}
		}
	case itemsImported:
		if err := validateGroupItemTargets(conn, dump.GroupItems); err != nil {
			return err
		}
	case groupsImported:
		if err := validateExistingGroupItems(conn, dump.Groups); err != nil {
			return err
		}
	}
	if err := validateChannelGroupReferences(conn, dump, authority); err != nil {
		return err
	}
	if err := validateRelayLogSectionDependencies(logConn, dump, authority); err != nil {
		return err
	}
	if err := validateUnownedChildSectionDependencies(conn, dump, authority); err != nil {
		return err
	}
	return nil
}

func validateFullChannelReferenceFields(dump *model.DBDump, authority map[string]bool) error {
	requiredSections := []string{
		"channel_keys", "group_items", "channel_model_capabilities", "channel_model_limits",
		"channel_probe_runs", "channel_probe_results",
		"stats_channel", "stats_model", "stats_daily_channel", "stats_daily_channel_model",
		"runtime_states", "circuit_breaker_states", "group_health_attempts", "relay_log_attempts",
	}
	positiveChannelIDSections := map[string]bool{
		"channel_keys": true, "group_items": true,
		"channel_model_capabilities": true, "channel_model_limits": true,
		"channel_probe_runs": true, "channel_probe_results": true,
		"stats_channel": true, "stats_daily_channel": true, "stats_daily_channel_model": true,
		"runtime_states": true, "circuit_breaker_states": true, "group_health_attempts": true,
	}
	for _, section := range requiredSections {
		if !sectionHasRows(dump, authority, section) {
			continue
		}
		rows, ok := sectionRowsByName(dump, section)
		if !ok {
			return fmt.Errorf("full restore channel reference section %q is unsupported", section)
		}
		for index := 0; index < sectionRowCount(rows); index++ {
			if !rowHasNonNullField(dump, section, index, "channel_id") {
				return fmt.Errorf("full restore %s row %d: channel_id is required and cannot be omitted or null", section, index)
			}
			if positiveChannelIDSections[section] {
				channelID, ok := sectionRowChannelID(rows, index)
				if !ok {
					return fmt.Errorf("full restore %s row %d: channel_id cannot be read", section, index)
				}
				if channelID <= 0 {
					return fmt.Errorf("full restore %s row %d: channel_id must be positive, got %d", section, index, channelID)
				}
			}
		}
	}
	return nil
}

func sectionRowChannelID(rows any, index int) (int, bool) {
	value := reflect.ValueOf(rows)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return 0, false
	}
	value = value.Elem()
	if value.Kind() != reflect.Slice || index < 0 || index >= value.Len() {
		return 0, false
	}
	field := value.Index(index).FieldByName("ChannelID")
	if !field.IsValid() || field.Kind() != reflect.Int {
		return 0, false
	}
	return int(field.Int()), true
}

func validateRelayLogSectionDependencies(conn *gorm.DB, dump *model.DBDump, authority map[string]bool) error {
	replacesLogs := sectionHasRows(dump, authority, "relay_logs")
	replacesBlobs := sectionHasRows(dump, authority, "relay_log_content_blobs")
	if !replacesLogs && !replacesBlobs {
		return nil
	}
	if !relayLogSectionDependenciesNeedTargetLookup(dump, authority) {
		return nil
	}
	if conn == nil {
		return fmt.Errorf("full restore cannot validate unowned relay-log dependencies because the log database is unavailable")
	}

	if replacesLogs {
		incomingLogIDs := make(map[int64]struct{}, len(dump.RelayLogs))
		for _, relayLog := range dump.RelayLogs {
			incomingLogIDs[relayLog.ID] = struct{}{}
		}
		for _, child := range []struct {
			section string
			table   string
		}{
			{"relay_log_attempts", "relay_log_attempts"},
			{"relay_log_content_refs", "relay_log_content_refs"},
		} {
			if sectionHasRows(dump, authority, child.section) || !conn.Migrator().HasTable(child.table) {
				continue
			}
			var parentIDs []int64
			if err := conn.Table(child.table).Distinct("relay_log_id").Pluck("relay_log_id", &parentIDs).Error; err != nil {
				return fmt.Errorf("inspect unowned %s parent references before replacing relay_logs: %w", child.section, err)
			}
			for _, relayLogID := range parentIDs {
				if _, remains := incomingLogIDs[relayLogID]; !remains {
					return fmt.Errorf(
						"full restore replaces relay_logs but unowned section %q still references removed relay_log id %d; include that section or retain the parent",
						child.section, relayLogID,
					)
				}
			}
		}
	}

	if replacesBlobs && !sectionHasRows(dump, authority, "relay_log_content_refs") &&
		conn.Migrator().HasTable("relay_log_content_refs") {
		incomingDigests := make(map[string]struct{}, len(dump.RelayLogContentBlobs))
		for _, blob := range dump.RelayLogContentBlobs {
			incomingDigests[blob.Digest] = struct{}{}
		}
		var refs []model.RelayLogContentRef
		if err := conn.Model(&model.RelayLogContentRef{}).
			Select("id", "blob_digest").
			Where("state = ?", model.RelayLogContentStateReady).
			Find(&refs).Error; err != nil {
			return fmt.Errorf("inspect unowned ready relay-log content references before replacing blobs: %w", err)
		}
		for _, ref := range refs {
			if _, remains := incomingDigests[ref.BlobDigest]; !remains {
				return fmt.Errorf(
					"full restore replaces relay_log_content_blobs but unowned ready relay_log_content_refs row %d points to absent blob %q; include relay_log_content_refs or retain the blob",
					ref.ID, ref.BlobDigest,
				)
			}
		}
	}
	return nil
}

func relayLogSectionDependenciesNeedTargetLookup(dump *model.DBDump, authority map[string]bool) bool {
	replacesLogs := sectionHasRows(dump, authority, "relay_logs")
	replacesBlobs := sectionHasRows(dump, authority, "relay_log_content_blobs")
	return (replacesLogs &&
		(!sectionHasRows(dump, authority, "relay_log_attempts") ||
			!sectionHasRows(dump, authority, "relay_log_content_refs"))) ||
		(replacesBlobs && !sectionHasRows(dump, authority, "relay_log_content_refs"))
}

func v6PreflightNeedsLogDBLookup(conn *gorm.DB, dump *model.DBDump, authority map[string]bool) (bool, error) {
	if relayLogSectionDependenciesNeedTargetLookup(dump, authority) {
		return true, nil
	}
	if !sectionHasRows(dump, authority, "channels") ||
		(sectionHasRows(dump, authority, "relay_logs") &&
			sectionHasRows(dump, authority, "relay_log_attempts")) {
		return false, nil
	}
	changedIDs, err := changedChannelIDs(conn, dump)
	if err != nil {
		return false, err
	}
	return len(changedIDs) > 0, nil
}

// validateUnownedChildSectionDependencies protects parent rows that are being
// replaced while their child collection remains owned by the target. The v6
// delete order already removes explicitly owned children first; this preflight
// covers omitted/null child sections, including an explicit empty parent array.
func validateUnownedChildSectionDependencies(conn *gorm.DB, dump *model.DBDump, authority map[string]bool) error {
	if conn == nil || dump == nil {
		return fmt.Errorf("full restore dependency check requires a database and backup")
	}
	checks := []struct {
		parentSection string
		childSection  string
		childTable    string
		parentColumn  string
		incomingIDs   func() []int64
	}{
		{
			parentSection: "channel_probe_runs",
			childSection:  "channel_probe_results",
			childTable:    "channel_probe_results",
			parentColumn:  "run_id",
			incomingIDs: func() []int64 {
				ids := make([]int64, 0, len(dump.ChannelProbeRuns))
				for _, row := range dump.ChannelProbeRuns {
					ids = append(ids, row.ID)
				}
				return ids
			},
		},
		{
			parentSection: "group_health_snapshots",
			childSection:  "group_health_attempts",
			childTable:    "group_health_attempts",
			parentColumn:  "snapshot_id",
			incomingIDs: func() []int64 {
				ids := make([]int64, 0, len(dump.GroupHealthSnapshots))
				for _, row := range dump.GroupHealthSnapshots {
					ids = append(ids, int64(row.ID))
				}
				return ids
			},
		},
	}
	for _, check := range checks {
		if !sectionHasRows(dump, authority, check.parentSection) ||
			sectionHasRows(dump, authority, check.childSection) ||
			!conn.Migrator().HasTable(check.childTable) {
			continue
		}
		parentIDs := check.incomingIDs()
		incoming := make(map[int64]bool, len(parentIDs))
		for _, id := range parentIDs {
			incoming[id] = true
		}
		var referencedIDs []int64
		if err := conn.Table(check.childTable).
			Distinct(check.parentColumn).
			Pluck(check.parentColumn, &referencedIDs).Error; err != nil {
			return fmt.Errorf("inspect unowned %s parent references before replacing %s: %w", check.childSection, check.parentSection, err)
		}
		sort.Slice(referencedIDs, func(i, j int) bool { return referencedIDs[i] < referencedIDs[j] })
		for _, parentID := range referencedIDs {
			if !incoming[parentID] {
				return fmt.Errorf(
					"full restore replaces %s but unowned section %q still references removed parent id %d; include that section or retain the parent",
					check.parentSection, check.childSection, parentID,
				)
			}
		}
	}
	return nil
}

func validateChannelGroupReferences(conn *gorm.DB, dump *model.DBDump, authority map[string]bool) error {
	channelGroupsOwned := sectionHasRows(dump, authority, "channel_groups")
	channelsOwned := sectionHasRows(dump, authority, "channels")
	if !channelGroupsOwned && !channelsOwned {
		return nil
	}

	availableGroups := make(map[int]bool)
	defaultGroupID := 0
	hasDefaultGroup := false
	if channelGroupsOwned {
		for _, group := range dump.ChannelGroups {
			availableGroups[group.ID] = true
			if group.IsDefault {
				defaultGroupID = group.ID
				hasDefaultGroup = true
			}
		}
	} else {
		var groups []model.ChannelGroup
		if err := conn.Select("id", "is_default").Find(&groups).Error; err != nil {
			return fmt.Errorf("inspect target channel groups before full restore: %w", err)
		}
		for _, group := range groups {
			availableGroups[group.ID] = true
			if group.IsDefault {
				defaultGroupID = group.ID
				hasDefaultGroup = true
			}
		}
	}
	groupIDs := make([]int, 0, len(availableGroups))
	for id := range availableGroups {
		groupIDs = append(groupIDs, id)
	}
	sort.Ints(groupIDs)
	check := func(channelID, groupID int) error {
		if groupID == 0 && hasDefaultGroup {
			return nil
		}
		if !availableGroups[groupID] {
			if channelGroupsOwned {
				return fmt.Errorf(
					"full restore channel id %d references channel group id %d absent from the imported channel_groups section (available IDs %v, default group present %t)",
					channelID, groupID, groupIDs, hasDefaultGroup,
				)
			}
			return fmt.Errorf(
				"full restore channel id %d references channel group id %d absent from target channel_groups collection (available IDs %v, default group present %t)",
				channelID, groupID, groupIDs, hasDefaultGroup,
			)
		}
		return nil
	}

	if channelsOwned {
		for index := range dump.Channels {
			channel := &dump.Channels[index]
			if channel.GroupID == 0 && hasDefaultGroup && defaultGroupID != 0 {
				channel.GroupID = defaultGroupID
			}
			if err := check(channel.ID, channel.GroupID); err != nil {
				return err
			}
		}
		return nil
	}
	var channels []model.Channel
	if err := conn.Select("id", "group_id").Find(&channels).Error; err != nil {
		return fmt.Errorf("inspect target channels before full restore: %w", err)
	}
	for _, channel := range channels {
		if err := check(channel.ID, channel.GroupID); err != nil {
			if channelGroupsOwned {
				return fmt.Errorf("full restore has unowned target channels: %w; include channels or retain the referenced group", err)
			}
			return err
		}
	}
	return nil
}

func changedChannelIDs(conn *gorm.DB, dump *model.DBDump) ([]int, error) {
	var targetChannels []model.Channel
	if err := conn.Select("id", "channel_uid").Find(&targetChannels).Error; err != nil {
		return nil, fmt.Errorf("inspect existing channel identities before full restore: %w", err)
	}
	targetByID := make(map[int]model.Channel, len(targetChannels))
	sourceByID := make(map[int]model.Channel, len(dump.Channels))
	for _, channel := range targetChannels {
		targetByID[channel.ID] = channel
	}
	for _, channel := range dump.Channels {
		sourceByID[channel.ID] = channel
	}
	changed := make(map[int]bool)
	for id, target := range targetByID {
		source, exists := sourceByID[id]
		if !exists || target.ChannelUID != source.ChannelUID {
			changed[id] = true
		}
	}
	for id, source := range sourceByID {
		target, exists := targetByID[id]
		if !exists || target.ChannelUID != source.ChannelUID {
			changed[id] = true
		}
	}
	ids := make([]int, 0, len(changed))
	for id := range changed {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids, nil
}

func validateUnownedChannelReferences(conn *gorm.DB, dump *model.DBDump, authority map[string]bool, changedIDs []int, logConn *gorm.DB) error {
	tables := []struct {
		section string
		table   string
		column  string
	}{
		{"channel_keys", "channel_keys", "channel_id"},
		{"group_items", "group_items", "channel_id"},
		{"channel_model_capabilities", "channel_model_capabilities", "channel_id"},
		{"channel_model_limits", "channel_model_limits", "channel_id"},
		{"channel_probe_runs", "channel_probe_runs", "channel_id"},
		{"channel_probe_results", "channel_probe_results", "channel_id"},
		{"stats_channel", "stats_channels", "channel_id"},
		{"stats_model", "stats_models", "channel_id"},
		{"stats_daily_channel", "stats_daily_channels", "channel_id"},
		{"stats_daily_channel_model", "stats_daily_channel_models", "channel_id"},
		{"runtime_states", "auto_strategy_states", "channel_id"},
		{"circuit_breaker_states", "circuit_breaker_states", "channel_id"},
		{"group_health_attempts", "group_health_attempts", "channel_id"},
	}
	for _, reference := range tables {
		if sectionHasRows(dump, authority, reference.section) || !conn.Migrator().HasTable(reference.table) {
			continue
		}
		var count int64
		if err := conn.Table(reference.table).Where(reference.column+" IN ?", changedIDs).Count(&count).Error; err != nil {
			return fmt.Errorf("check unowned %s channel references: %w", reference.section, err)
		}
		if count > 0 {
			return fmt.Errorf(
				"full restore changes channel identities referenced by %d target rows in unowned section %q; include that section or keep the channel identities unchanged",
				count, reference.section,
			)
		}
	}
	if !sectionHasRows(dump, authority, "group_health_snapshots") && conn.Migrator().HasTable("group_health_snapshots") {
		var count int64
		if err := conn.Table("group_health_snapshots").Where("successful_channel_id IN ?", changedIDs).Count(&count).Error; err != nil {
			return fmt.Errorf("check unowned group_health_snapshots channel references: %w", err)
		}
		if count > 0 {
			return fmt.Errorf(
				"full restore changes channel identities referenced by %d target rows in unowned section %q; include that section or keep the channel identities unchanged",
				count, "group_health_snapshots",
			)
		}
	}
	if err := validateUnownedRelayLogReferences(logConn, dump, authority, changedIDs); err != nil {
		return err
	}
	if conn.Migrator().HasTable("ws_response_affinities") {
		var count int64
		if err := conn.Table("ws_response_affinities").Where("channel_id IN ?", changedIDs).Count(&count).Error; err != nil {
			return fmt.Errorf("check response affinity channel references: %w", err)
		}
		if count > 0 {
			return fmt.Errorf(
				"full restore changes channel identities referenced by %d target response affinities; clear or preserve those channel identities before restoring",
				count,
			)
		}
	}
	if !sectionHasRows(dump, authority, "ai_route_tasks") && conn.Migrator().HasTable("ai_route_tasks") {
		var tasks []model.AIRouteTask
		if err := conn.Find(&tasks).Error; err != nil {
			return fmt.Errorf("check unowned AI route task channel references: %w", err)
		}
		for _, task := range tasks {
			references := make([]int, 0)
			add := func(id int) {
				if id > 0 {
					references = append(references, id)
				}
			}
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
			for _, id := range references {
				if containsInt(changedIDs, id) {
					return fmt.Errorf(
						"full restore changes channel id %d referenced by an unowned AI route task; include ai_route_tasks or keep the channel identity unchanged",
						id,
					)
				}
			}
		}
	}
	return nil
}

func validateUnownedRelayLogReferences(conn *gorm.DB, dump *model.DBDump, authority map[string]bool, changedIDs []int) error {
	checkRelayLogs := !sectionHasRows(dump, authority, "relay_logs")
	checkAttempts := !sectionHasRows(dump, authority, "relay_log_attempts")
	if !checkRelayLogs && !checkAttempts {
		return nil
	}
	if conn == nil {
		return fmt.Errorf("full restore cannot verify unowned relay-log channel references because the separate log database is unavailable")
	}

	if checkRelayLogs && conn.Migrator().HasTable("relay_logs") {
		var count int64
		if err := conn.Model(&model.RelayLog{}).Where("channel_id IN ?", changedIDs).Count(&count).Error; err != nil {
			return fmt.Errorf("check unowned relay_logs channel references: %w", err)
		}
		if count > 0 {
			return fmt.Errorf(
				"full restore changes channel identities referenced by %d target rows in unowned section %q; include that section or keep the channel identities unchanged",
				count, "relay_logs",
			)
		}

		var nestedReferences int64
		var rows []model.RelayLog
		if err := conn.Model(&model.RelayLog{}).
			Select("id", "attempts").
			FindInBatches(&rows, 500, func(_ *gorm.DB, _ int) error {
				for _, row := range rows {
					for _, attempt := range row.Attempts {
						if containsInt(changedIDs, attempt.ChannelID) {
							nestedReferences++
						}
					}
				}
				return nil
			}).Error; err != nil {
			return fmt.Errorf("check unowned relay_logs nested attempt channel references: %w", err)
		}
		if nestedReferences > 0 {
			return fmt.Errorf(
				"full restore changes channel identities referenced by %d nested attempts in unowned section %q; include that section or keep the channel identities unchanged",
				nestedReferences, "relay_logs",
			)
		}
	}

	if checkAttempts && conn.Migrator().HasTable("relay_log_attempts") {
		var count int64
		if err := conn.Model(&model.RelayLogAttempt{}).Where("channel_id IN ?", changedIDs).Count(&count).Error; err != nil {
			return fmt.Errorf("check unowned relay_log_attempts channel references: %w", err)
		}
		if count > 0 {
			return fmt.Errorf(
				"full restore changes channel identities referenced by %d target rows in unowned section %q; include that section or keep the channel identities unchanged",
				count, "relay_log_attempts",
			)
		}
	}
	return nil
}

func validateGroupItemTargets(conn *gorm.DB, items []model.GroupItem) error {
	if len(items) == 0 {
		return nil
	}
	var groups []model.Group
	if err := conn.Select("id").Find(&groups).Error; err != nil {
		return fmt.Errorf("resolve target groups for full restore group_items: %w", err)
	}
	existing := make(map[int]bool, len(groups))
	for _, group := range groups {
		existing[group.ID] = true
	}
	for _, item := range items {
		if !existing[item.GroupID] {
			return fmt.Errorf("full restore group_items references group id %d absent from the target; include the groups section", item.GroupID)
		}
	}
	return nil
}

func validateExistingGroupItems(conn *gorm.DB, groups []model.Group) error {
	var items []model.GroupItem
	if err := conn.Select("group_id").Find(&items).Error; err != nil {
		return fmt.Errorf("inspect existing group_items before replacing groups: %w", err)
	}
	incoming := make(map[int]bool, len(groups))
	for _, group := range groups {
		incoming[group.ID] = true
	}
	for _, item := range items {
		if !incoming[item.GroupID] {
			return fmt.Errorf("full restore replaces groups but unowned target group_items still reference removed group id %d; include group_items", item.GroupID)
		}
	}
	return nil
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
// the dry-run counterpart used by the management import endpoint and accepts
// the same optional administrator mapping as the actual import.
func PreviewChannelMapping(ctx context.Context, dump *model.DBDump, mode string, explicit map[int]int) (map[int]int, []model.DBChannelMapping, error) {
	if dump == nil {
		return nil, nil, fmt.Errorf("empty dump")
	}
	if mode != model.ImportModeFull && mode != model.ImportModeIncremental {
		return nil, nil, fmt.Errorf("unsupported import mode")
	}
	authority, err := authoritativeSections(dump)
	if err != nil {
		return nil, nil, err
	}
	importChannels := sectionHasRows(dump, authority, "channels")
	preserveMissingUIDs := map[int]bool(nil)
	missingUID := make(map[int]bool, len(dump.Channels))
	if importChannels {
		preserveMissingUIDs = missingChannelUIDPatchRows(dump, mode == model.ImportModeFull)
		for index, channel := range dump.Channels {
			if channel.ChannelUID == "" && !preserveMissingUIDs[index] {
				missingUID[channel.ID] = true
			}
		}
		if err := ensureDumpChannelUIDs(dump, preserveMissingUIDs); err != nil {
			return nil, nil, err
		}
	}
	mapping, mappings, err := channelIDMapping(ctx, db.GetDB(), dump, mode == model.ImportModeFull, authority, explicit, missingUID)
	return mapping, mappings, err
}
