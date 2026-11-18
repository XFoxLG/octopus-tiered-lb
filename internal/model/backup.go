package model

import "time"

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
//     accounts and Passkeys stay owned by the target deployment. A top-level
//     "sections" manifest makes collection authority explicit: a listed section
//     is replaced (full restore) or updated (incremental import), while a
//     missing section keeps the target collection untouched. A listed section
//     carrying JSON null also means "untouched"; an explicit [] clears it.
//     Channels are matched by channel_uid before numeric ID, and GroupItem /
//     ChannelKey foreign keys follow the resolved source-to-target mapping.
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
