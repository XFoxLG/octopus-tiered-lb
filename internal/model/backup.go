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

	RelayLogs []RelayLog `json:"relay_logs,omitempty"`

	// API credential profiles (Tools: CLI credential verification/export)
	APICredentialProfiles []APICredentialProfile `json:"api_credential_profiles,omitempty"`
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

type DatabaseMigrationRequest struct {
	Type         string `json:"type"`
	Path         string `json:"path"`
	IncludeLogs  bool   `json:"include_logs"`
	IncludeStats bool   `json:"include_stats"`
}

type DatabaseMigrationResult struct {
	Type          string `json:"type"`
	Path          string `json:"path"`
	IncludeLogs   bool   `json:"include_logs"`
	IncludeStats  bool   `json:"include_stats"`
	RestartNeeded bool   `json:"restart_needed"`
	// CleanedFiles 迁移成功后已删除的旧 SQLite 文件路径（issue #118）。
	// 仅当源库为 SQLite、目标库为非 SQLite 时非空。
	CleanedFiles []string       `json:"cleaned_files"`
	ImportResult DBImportResult `json:"import_result"`
}

// CacheConfig 描述当前缓存后端配置（config.json 的 cache 字段镜像）。
// Type 为空表示内存模式（向后兼容），"redis" 表示启用 Redis 后端（issue #123）。
type CacheConfig struct {
	Type  string           `json:"type"`
	Redis CacheRedisConfig `json:"redis"`
}

// CacheRedisConfig 是 CacheConfig 内的 Redis 连接参数（与 conf.RedisConfig 同构，
// 独立定义避免 model 包反向依赖 conf 包）。
// DialTimeout/ReadTimeout 用字符串（如 "3s"）便于前端输入；空串表示用默认值。
type CacheRedisConfig struct {
	Addr        string `json:"addr"`
	Password    string `json:"password"`
	Username    string `json:"username"`
	DB          int    `json:"db"`
	PoolSize    int    `json:"pool_size"`
	DialTimeout string `json:"dial_timeout"`
	ReadTimeout string `json:"read_timeout"`
}

// CacheConfigRequest 用于测试连接 / 保存配置（POST body）。
type CacheConfigRequest struct {
	Type  string           `json:"type"`
	Redis CacheRedisConfig `json:"redis"`
}

// CacheConfigResult 保存配置后的响应。重启后生效（与数据库迁移一致）。
type CacheConfigResult struct {
	Type          string `json:"type"`
	RestartNeeded bool   `json:"restart_needed"`
}
