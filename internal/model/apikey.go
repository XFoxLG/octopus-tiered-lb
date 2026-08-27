package model

type APIKey struct {
	ID                     int     `json:"id" gorm:"primaryKey"`
	Name                   string  `json:"name" gorm:"not null"`
	APIKey                 string  `json:"api_key" gorm:"not null;uniqueIndex;size:512"`                         // 加密密文（enc: 前缀）；存量哈希值不可逆，标记需重生
	APIKeyHash             string  `json:"-" gorm:"not null;uniqueIndex;size:64;column:api_key_hash;default:''"` // SHA-256(明文)，确定性查找列，不返回前端
	Enabled                bool    `json:"enabled" gorm:"default:true"`
	ExpireAt               int64   `json:"expire_at,omitempty"`
	MaxCost                float64 `json:"max_cost,omitempty"`
	MaxTokens              int64   `json:"max_tokens,omitempty" gorm:"default:0"` // Token 用量上限（0=不限制），issue #108
	SupportedModels        string  `json:"supported_models,omitempty"`
	AllowedGroupCategories string  `json:"allowed_group_categories,omitempty" gorm:"column:allowed_group_categories"` // 逗号分隔的允许分组分类，空表示全部
	RateLimitRPM           int     `json:"rate_limit_rpm,omitempty" gorm:"default:0"`
	RateLimitTPM           int     `json:"rate_limit_tpm,omitempty" gorm:"default:0"`
	PerModelQuotaJSON      string  `json:"per_model_quota_json,omitempty" gorm:"column:per_model_quota_json"`
	AllowedIPs             string  `json:"allowed_ips,omitempty" gorm:"column:allowed_ips"`             // 逗号分隔的允许 IP/CIDR 列表
	Tags                   string  `json:"tags,omitempty" gorm:"column:tags"`                           // 逗号分隔的标签，用于分类与快速检索
	ExcludedChannels       string  `json:"excluded_channels,omitempty" gorm:"column:excluded_channels"` // 逗号分隔的被排除渠道 ID，该 Key 不会命中这些渠道（issue #55）
}
