package model

import "time"

const (
	ImportModeIncremental = "incremental" // insert new, skip existing
	ImportModeFull        = "full"        // delete all then insert
)

// DBDump is a full-database JSON export format for Octopus.
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

	Notifications []Notification `json:"notifications,omitempty"`

	AuditLogs            []AuditLog            `json:"audit_logs,omitempty"`
	RuntimeStates        []AutoStrategyState   `json:"runtime_states,omitempty"`
	CircuitBreakerStates []CircuitBreakerState `json:"circuit_breaker_states,omitempty"`

	StatsTotal   []StatsTotal   `json:"stats_total,omitempty"`
	StatsDaily   []StatsDaily   `json:"stats_daily,omitempty"`
	StatsHourly  []StatsHourly  `json:"stats_hourly,omitempty"`
	StatsModel   []StatsModel   `json:"stats_model,omitempty"`
	StatsChannel []StatsChannel `json:"stats_channel,omitempty"`
	StatsAPIKey  []StatsAPIKey  `json:"stats_api_key,omitempty"`

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
