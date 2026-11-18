package model

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	utilsjson "github.com/lingyuins/octopus/internal/utils/json"
)

const (
	ImportModeIncremental = "incremental" // insert new, skip existing
	ImportModeFull        = "full"        // delete all then insert
)

// DBDump is a versioned application-data backup, not a database schema snapshot.
// Deployment-owned authentication/cache credentials and migration records are
// deliberately outside this format; use a database backup for those.
//
// Version history:
//   - v1: original format.
//   - v2 (2026-09-11): relay log family split — relay_log_attempts /
//     relay_log_content_refs / relay_log_content_blobs travel alongside
//     relay_logs.
//   - v4: proxy/model/pricing configuration, protocol evidence, detailed daily
//     statistics and error logs are preserved. Full v1-v3 restores preserve
//     these newer tables when they are absent from the historical dump.
//   - v3: sensitive fields (api_keys.api_key,
//     channel_keys.channel_key, api_credential_profiles.api_key) are exported
//     as plaintext and idempotently re-encrypted with the importing
//     instance's crypto key (empty values and already-enc: values pass
//     through unchanged). Plaintext api_keys additionally get api_key_hash
//     recomputed (SHA-256 hex) on import. v1/v2 dumps carry the source
//     instance's enc: ciphertext and stay restorable on the same instance
//     (issue #247). **Backup files now contain plaintext secrets — store
//     them accordingly.**
//   - v5 (2026-10-28): group health snapshots/attempts (group_health_snapshots,
//     group_health_attempts) and AI route tasks (ai_route_tasks) are exported
//     and restored. Full v1-v4 restores preserve target rows in these tables
//     when the historical dump has no data for them; full v5 restores delete
//     and rebuild them authoritatively.
//   - v6 (2026-11-18): configuration import format. Administrator identity
//     rows are never exported and are ignored with a warning on import; admin
//     accounts, Passkeys, and WebAuthn relying-party settings stay owned by the
//     target deployment. A top-level "sections" manifest makes collection
//     authority explicit: a listed section is replaced (full restore) or
//     updated (incremental import), while a missing section keeps the target
//     collection untouched. A listed section carrying JSON null also means
//     "untouched"; an explicit [] clears it. Channels are matched by
//     channel_uid before numeric ID, and GroupItem / ChannelKey foreign keys
//     follow the resolved source-to-target mapping.
type DBDump struct {
	Version      int       `json:"version"`
	ExportedAt   time.Time `json:"exported_at"`
	IncludeLogs  bool      `json:"include_logs"`
	IncludeStats bool      `json:"include_stats"`

	// Sections is the v6 collection-authority manifest. It is empty for
	// v1-v5 dumps, which keep their historical import semantics.
	Sections []string `json:"sections"`

	Channels      []Channel      `json:"channels"`
	ChannelKeys   []ChannelKey   `json:"channel_keys"`
	ChannelGroups []ChannelGroup `json:"channel_groups"`
	Groups        []Group        `json:"groups"`
	GroupItems    []GroupItem    `json:"group_items"`
	LLMInfos      []LLMInfo      `json:"llm_infos"`
	APIKeys       []APIKey       `json:"api_keys"`
	Users         []User         `json:"users,omitempty"`
	Settings      []Setting      `json:"settings"`

	ProxyConfigurations      []ProxyConfiguration     `json:"proxy_configurations"`
	ModelMappings            []ModelMapping           `json:"model_mappings"`
	ModelPriceCategories     []ModelPriceCategory     `json:"model_price_categories"`
	ModelPriceSchedules      []ModelPriceSchedule     `json:"model_price_schedules"`
	ChannelModelCapabilities []ChannelModelCapability `json:"channel_model_capabilities"`
	ChannelModelLimits       []ChannelModelLimit      `json:"channel_model_limits"`
	ChannelProbeRuns         []ChannelProbeRun        `json:"channel_probe_runs"`
	ChannelProbeResults      []ChannelProbeResult     `json:"channel_probe_results"`

	Notifications []Notification `json:"notifications"`

	ErrorLogs            []ErrorLog            `json:"error_logs"`
	AuditLogs            []AuditLog            `json:"audit_logs"`
	RuntimeStates        []AutoStrategyState   `json:"runtime_states"`
	CircuitBreakerStates []CircuitBreakerState `json:"circuit_breaker_states"`

	StatsTotal             []StatsTotal             `json:"stats_total"`
	StatsDaily             []StatsDaily             `json:"stats_daily"`
	StatsHourly            []StatsHourly            `json:"stats_hourly"`
	StatsModel             []StatsModel             `json:"stats_model"`
	StatsChannel           []StatsChannel           `json:"stats_channel"`
	StatsAPIKey            []StatsAPIKey            `json:"stats_api_key"`
	StatsDailyChannel      []StatsDailyChannel      `json:"stats_daily_channel"`
	StatsDailyModel        []StatsDailyModel        `json:"stats_daily_model"`
	StatsDailyAPIKey       []StatsDailyAPIKey       `json:"stats_daily_api_key"`
	StatsDailyChannelModel []StatsDailyChannelModel `json:"stats_daily_channel_model"`

	RelayLogs            []RelayLog                `json:"relay_logs"`
	RelayLogAttempts     []RelayLogAttempt         `json:"relay_log_attempts"`
	RelayLogContentRefs  []RelayLogContentRef      `json:"relay_log_content_refs"`
	RelayLogContentBlobs []RelayLogContentBlobDump `json:"relay_log_content_blobs"`

	GroupHealthSnapshots []GroupHealthSnapshot `json:"group_health_snapshots"`
	GroupHealthAttempts  []GroupHealthAttempt  `json:"group_health_attempts"`
	AIRouteTasks         []AIRouteTask         `json:"ai_route_tasks"`

	// API credential profiles (Tools: CLI credential verification/export)
	APICredentialProfiles []APICredentialProfile `json:"api_credential_profiles"`

	// rowFieldPresence records which JSON fields actually appeared in each row.
	// It is populated by UnmarshalJSON and deliberately excluded from exports.
	// v6 incremental imports use it to patch only explicit non-null fields while
	// preserving target values for missing/null fields and explicit zero values.
	rowFieldPresence map[string][]DBDumpRowFieldPresence
	// Relay-log attempts need only a compact channel_id presence bit for full
	// restore validation; retaining a map for every field in potentially large
	// attempt arrays needlessly multiplies backup memory use.
	relayLogAttemptChannelIDNonNull []bool
	// nestedData keeps a management API response envelope's data object until
	// the handler can decode it. The value is never exported.
	nestedData json.RawMessage
}

// DBDumpRowFieldPresence describes one decoded backup row without retaining its
// payload. Present distinguishes an omitted field from an explicit zero value;
// NonNull excludes JSON null, which means "leave the target value unchanged"
// during v6 incremental imports.
type DBDumpRowFieldPresence struct {
	Present map[string]bool
	NonNull map[string]bool
}

// RowFieldPresence returns the recorded field-presence metadata for one row.
// Programmatically constructed dumps have no metadata and keep the historical
// complete-row import behavior.
func (d *DBDump) RowFieldPresence(section string, index int) (DBDumpRowFieldPresence, bool) {
	if d == nil {
		return DBDumpRowFieldPresence{}, false
	}
	rows, ok := d.rowFieldPresence[section]
	if !ok || index < 0 || index >= len(rows) {
		return DBDumpRowFieldPresence{}, false
	}
	return rows[index], true
}

// RelayLogAttemptChannelIDNonNull reports whether a decoded v6 attempt row
// supplied a non-null channel_id. Programmatic dumps have no tracked metadata.
func (d *DBDump) RelayLogAttemptChannelIDNonNull(index int) (bool, bool) {
	if d == nil || index < 0 || index >= len(d.relayLogAttemptChannelIDNonNull) {
		return false, false
	}
	return d.relayLogAttemptChannelIDNonNull[index], true
}

// NestedData returns the wrapper response's data payload when one was present
// during JSON decoding. The payload is immutable; callers must not modify it.
// Direct backup objects return nil.
func (d *DBDump) NestedData() json.RawMessage {
	if d == nil || len(d.nestedData) == 0 {
		return nil
	}
	return d.nestedData
}

// UnmarshalJSON preserves collection null-vs-array behavior and records row-level
// field presence. The latter is required for v6 incremental patch semantics:
// decoding directly into concrete structs otherwise collapses omitted fields,
// explicit nulls and explicit zero values into the same Go value.
func (d *DBDump) UnmarshalJSON(data []byte) error {
	type dbDumpAlias DBDump
	var decoded dbDumpAlias
	if err := utilsjson.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	if raw, ok := object["data"]; ok {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
			decoded.nestedData = trimmed
		}
	}
	if decoded.Version < 6 {
		decoded.rowFieldPresence = nil
		decoded.relayLogAttemptChannelIDNonNull = nil
		*d = DBDump(decoded)
		return nil
	}
	presence := make(map[string][]DBDumpRowFieldPresence)
	for section, raw := range object {
		if section == "relay_log_attempts" {
			trimmed := bytes.TrimSpace(raw)
			if len(trimmed) > 0 && trimmed[0] == '[' {
				var entries []json.RawMessage
				if err := json.Unmarshal(trimmed, &entries); err != nil {
					return err
				}
				channelIDs := make([]bool, len(entries))
				for index, entry := range entries {
					rowBytes := bytes.TrimSpace(entry)
					if len(rowBytes) == 0 || rowBytes[0] != '{' {
						continue
					}
					var fields map[string]json.RawMessage
					if err := json.Unmarshal(rowBytes, &fields); err != nil {
						return err
					}
					value, exists := fields["channel_id"]
					channelIDs[index] = exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null"))
				}
				decoded.relayLogAttemptChannelIDNonNull = channelIDs
			}
			continue
		}
		if !requiresRowFieldPresence(section) {
			continue
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			continue
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			continue
		}
		// Only object arrays can represent database rows. The "sections"
		// manifest is a string array and is intentionally ignored here.
		rows := make([]DBDumpRowFieldPresence, 0, len(entries))
		isRowArray := true
		for _, entry := range entries {
			rowBytes := bytes.TrimSpace(entry)
			if len(rowBytes) == 0 || bytes.Equal(rowBytes, []byte("null")) {
				rows = append(rows, DBDumpRowFieldPresence{})
				continue
			}
			if rowBytes[0] != '{' {
				isRowArray = false
				break
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(rowBytes, &fields); err != nil {
				return err
			}
			row := DBDumpRowFieldPresence{
				Present: make(map[string]bool, len(fields)),
				NonNull: make(map[string]bool, len(fields)),
			}
			for field, value := range fields {
				row.Present[field] = true
				if !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					row.NonNull[field] = true
				}
			}
			rows = append(rows, row)
		}
		if isRowArray {
			presence[section] = rows
		}
	}
	decoded.rowFieldPresence = presence
	*d = DBDump(decoded)
	return nil
}

// MarshalJSON emits canonical v6 backups as complete rows. Struct-level
// omitempty tags are intentionally suppressed for row fields so an exported
// zero, false or empty string remains distinguishable from a missing field when
// the backup is later applied incrementally. Association-only fields and
// json:"-" internals are omitted.
func (d DBDump) MarshalJSON() ([]byte, error) {
	value := reflect.ValueOf(d)
	typ := value.Type()
	object := make(map[string]any, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		structField := typ.Field(index)
		if structField.PkgPath != "" {
			continue
		}
		name, omitEmpty := parseJSONTag(structField)
		if name == "" {
			continue
		}
		field := value.Field(index)
		if omitEmpty && field.IsZero() {
			continue
		}
		if field.Kind() == reflect.Slice && field.Type().Elem().Kind() == reflect.Struct {
			rows := make([]any, field.Len())
			for rowIndex := 0; rowIndex < field.Len(); rowIndex++ {
				row, err := completeBackupRow(field.Index(rowIndex))
				if err != nil {
					return nil, err
				}
				rows[rowIndex] = row
			}
			object[name] = rows
			continue
		}
		object[name] = field.Interface()
	}
	return utilsjson.Marshal(object)
}

// MarshalBackupRow serializes one streamed backup row using the same complete
// field rules as DBDump.MarshalJSON. This preserves explicit zero values from
// database rows while excluding association-only and json:"-" fields.
func MarshalBackupRow(value any) ([]byte, error) {
	row, err := completeBackupRow(reflect.ValueOf(value))
	if err != nil {
		return nil, err
	}
	return utilsjson.Marshal(row)
}

func completeBackupRow(value reflect.Value) (map[string]any, error) {
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil, nil
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return nil, nil
	}
	object := make(map[string]any)
	if err := appendCompleteBackupFields(object, value); err != nil {
		return nil, err
	}
	return object, nil
}

func appendCompleteBackupFields(object map[string]any, value reflect.Value) error {
	typ := value.Type()
	for index := 0; index < typ.NumField(); index++ {
		structField := typ.Field(index)
		if structField.PkgPath != "" {
			continue
		}
		name, _ := parseJSONTag(structField)
		if name == "" || associationField(structField) {
			continue
		}
		field := value.Field(index)
		if structField.Anonymous && field.Kind() == reflect.Struct {
			if err := appendCompleteBackupFields(object, field); err != nil {
				return err
			}
			continue
		}
		object[name] = field.Interface()
	}
	return nil
}

func parseJSONTag(field reflect.StructField) (string, bool) {
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return field.Name, false
	}
	parts := strings.Split(tag, ",")
	if parts[0] == "-" {
		return "", false
	}
	name := parts[0]
	if name == "" {
		name = field.Name
	}
	omitEmpty := false
	for _, option := range parts[1:] {
		if option == "omitempty" {
			omitEmpty = true
			break
		}
	}
	return name, omitEmpty
}

func associationField(field reflect.StructField) bool {
	tag := field.Tag.Get("gorm")
	if tag == "-" {
		return true
	}
	return strings.Contains(tag, "foreignKey:") || strings.Contains(tag, "many2many:")
}

func requiresRowFieldPresence(section string) bool {
	switch section {
	case "error_logs", "audit_logs", "relay_logs",
		"relay_log_attempts", "relay_log_content_refs", "relay_log_content_blobs", "users":
		return false
	}
	return true
}

// RelayLogContentBlobDump exposes bytea payloads only inside the versioned
// backup format. Runtime APIs never serialize RelayLogContentBlob.Payload.
type RelayLogContentBlobDump struct {
	Digest       string `json:"digest"`
	Encoding     string `json:"encoding"`
	OriginalSize int64  `json:"original_size"`
	StoredSize   int64  `json:"stored_size"`
	Payload      []byte `json:"payload"`
	CreatedAt    int64  `json:"created_at"`
}

type DBImportResult struct {
	// RowsAffected contains the rows affected for each table.
	RowsAffected map[string]int64 `json:"rows_affected"`
	Progress     []DBImportStep   `json:"progress,omitempty"`
	// Warnings reports discarded or degraded input without failing the import.
	Warnings []string `json:"warnings,omitempty"`
	// ChannelMappings records source channel IDs that were resolved to a
	// different target channel through channel_uid.
	ChannelMappings []DBChannelMapping `json:"channel_mappings,omitempty"`
}

// DBChannelMapping is one audited source-to-target channel resolution.
type DBChannelMapping struct {
	SourceChannelID int    `json:"source_channel_id"`
	TargetChannelID int    `json:"target_channel_id"`
	ChannelUID      string `json:"channel_uid"`
	Reason          string `json:"reason"`
}

type DBImportStep struct {
	Table        string `json:"table"`
	Mode         string `json:"mode"` // "delete" or "insert"
	RowsAffected int64  `json:"rows_affected"`
	OK           bool   `json:"ok"`
	Error        string `json:"error,omitempty"`
}
