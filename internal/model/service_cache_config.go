package model

import "time"

// ServiceCacheConfig is deployment infrastructure, not an application backup
// setting. The owning Render service is resolved by the server, never the API.
type ServiceCacheConfig struct {
	ServiceID       string    `gorm:"primaryKey;size:128" json:"-"`
	EncryptedConfig string    `gorm:"type:text;not null" json:"-"`
	UpdatedAt       time.Time `json:"-"`
}

// CacheConfig exposes desired configuration separately from the active backend.
// Redis contains only safe display metadata, never a round-trippable secret.
type CacheConfig struct {
	Type               string            `json:"type"`
	Redis              CacheRedisSummary `json:"redis"`
	HasSavedConnection bool              `json:"has_saved_connection"`
	HasPassword        bool              `json:"has_password"`
	ConfigSource       string            `json:"config_source"`
	RuntimeBackend     string            `json:"runtime_backend"`
	RuntimeHealthy     bool              `json:"runtime_healthy"`
	RuntimeTLS         bool              `json:"runtime_tls"`
	RestartNeeded      bool              `json:"restart_needed"`
	Reconnecting       bool              `json:"reconnecting"`
}

type CacheRedisSummary struct {
	Addr        string `json:"addr"`
	DB          int    `json:"db"`
	TLS         bool   `json:"tls"`
	PoolSize    int    `json:"pool_size"`
	DialTimeout string `json:"dial_timeout"`
	ReadTimeout string `json:"read_timeout"`
}

// CacheRedisConfig replaces an entire connection when included in a request.
// Omission preserves the saved connection; a masked string is never a secret.
type CacheRedisConfig struct {
	Addr        string `json:"addr"`
	Password    string `json:"password"`
	Username    string `json:"username"`
	DB          int    `json:"db"`
	PoolSize    int    `json:"pool_size"`
	DialTimeout string `json:"dial_timeout"`
	ReadTimeout string `json:"read_timeout"`
	TLS         bool   `json:"tls"`
	CAFile      string `json:"ca_file"`
}

// CacheRedisTuning permits budget changes without resubmitting credentials or
// allowing the saved password to be sent to a different destination.
type CacheRedisTuning struct {
	PoolSize    int    `json:"pool_size"`
	DialTimeout string `json:"dial_timeout"`
	ReadTimeout string `json:"read_timeout"`
}

type CacheConfigRequest struct {
	Type   string            `json:"type"`
	Redis  *CacheRedisConfig `json:"redis,omitempty"`
	Tuning *CacheRedisTuning `json:"tuning,omitempty"`
}

type CacheConfigResult struct {
	Type          string `json:"type"`
	RestartNeeded bool   `json:"restart_needed"`
}
