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
type DBDump struct {
	Version      int       `json:"version"`
	ExportedAt   time.Time `json:"exported_at"`
	IncludeLogs  bool      `json:"include_logs"`
	IncludeStats bool      `json:"include_stats"`

	Channels      []Channel      `json:"channels,omitempty"`
	ChannelKeys   []ChannelKey   `json:"channel_keys,omitempty"`
	ChannelGroups []ChannelGroup `json:"channel_groups,omitempty"`
	Groups        []Group        `json:"groups,omitempty"`
	GroupItems    []GroupItem    `json:"group_items,omitempty"`
	LLMInfos      []LLMInfo      `json:"llm_infos,omitempty"`
	APIKeys       []APIKey       `json:"api_keys,omitempty"`
	Users         []User         `json:"users,omitempty"`
	Settings      []Setting      `json:"settings,omitempty"`

	ProxyConfigurations      []ProxyConfiguration     `json:"proxy_configurations,omitempty"`
	ModelMappings            []ModelMapping           `json:"model_mappings,omitempty"`
	ModelPriceCategories     []ModelPriceCategory     `json:"model_price_categories,omitempty"`
	ModelPriceSchedules      []ModelPriceSchedule     `json:"model_price_schedules,omitempty"`
	ChannelModelCapabilities []ChannelModelCapability `json:"channel_model_capabilities,omitempty"`
	ChannelModelLimits       []ChannelModelLimit      `json:"channel_model_limits,omitempty"`
	ChannelProbeRuns         []ChannelProbeRun        `json:"channel_probe_runs,omitempty"`
	ChannelProbeResults      []ChannelProbeResult     `json:"channel_probe_results,omitempty"`

	Notifications []Notification `json:"notifications,omitempty"`

	ErrorLogs            []ErrorLog            `json:"error_logs,omitempty"`
	AuditLogs            []AuditLog            `json:"audit_logs,omitempty"`
	RuntimeStates        []AutoStrategyState   `json:"runtime_states,omitempty"`
	CircuitBreakerStates []CircuitBreakerState `json:"circuit_breaker_states,omitempty"`

	StatsTotal             []StatsTotal             `json:"stats_total,omitempty"`
	StatsDaily             []StatsDaily             `json:"stats_daily,omitempty"`
	StatsHourly            []StatsHourly            `json:"stats_hourly,omitempty"`
	StatsModel             []StatsModel             `json:"stats_model,omitempty"`
	StatsChannel           []StatsChannel           `json:"stats_channel,omitempty"`
	StatsAPIKey            []StatsAPIKey            `json:"stats_api_key,omitempty"`
	StatsDailyChannel      []StatsDailyChannel      `json:"stats_daily_channel,omitempty"`
	StatsDailyModel        []StatsDailyModel        `json:"stats_daily_model,omitempty"`
	StatsDailyAPIKey       []StatsDailyAPIKey       `json:"stats_daily_api_key,omitempty"`
	StatsDailyChannelModel []StatsDailyChannelModel `json:"stats_daily_channel_model,omitempty"`

	RelayLogs            []RelayLog                `json:"relay_logs,omitempty"`
	RelayLogAttempts     []RelayLogAttempt         `json:"relay_log_attempts,omitempty"`
	RelayLogContentRefs  []RelayLogContentRef      `json:"relay_log_content_refs,omitempty"`
	RelayLogContentBlobs []RelayLogContentBlobDump `json:"relay_log_content_blobs,omitempty"`

	// API credential profiles (Tools: CLI credential verification/export)
	APICredentialProfiles []APICredentialProfile `json:"api_credential_profiles,omitempty"`
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
}

type DBImportStep struct {
	Table        string `json:"table"`
	Mode         string `json:"mode"` // "delete" or "insert"
	RowsAffected int64  `json:"rows_affected"`
	OK           bool   `json:"ok"`
	Error        string `json:"error,omitempty"`
}
