package model

import "time"

// 渠道×模型上下文上限的来源（v2.9.0）。
const (
	// ChannelModelLimitSourceManual 管理端人工声明：永不被学习覆盖。
	ChannelModelLimitSourceManual = "manual"
	// ChannelModelLimitSourceLearned 运行时学习：同一渠道×模型累计多次独立的上游
	// 上下文超限 400 后写入。
	ChannelModelLimitSourceLearned = "learned"
)

// ChannelModelLimit 记录「渠道 × 模型」的上下文上限。
// 用途：①请求前预检——估算输入 token 超过已知上限时直接排除该候选；
// ②学习——上游 400（context_length_exceeded 类）累计到阈值后写入 learned 上限，
// 避免 65K/110K prompt 反复被 32K 渠道抽中。
type ChannelModelLimit struct {
	ID               int       `json:"id" gorm:"primaryKey;autoIncrement"`
	ChannelID        int       `json:"channel_id" gorm:"column:channel_id;uniqueIndex:uidx_channel_model_limit,priority:1"`
	ModelName        string    `json:"model_name" gorm:"column:model_name;size:191;uniqueIndex:uidx_channel_model_limit,priority:2"`
	MaxContextTokens int       `json:"max_context_tokens" gorm:"column:max_context_tokens;not null;default:0"` // 0 = 未声明
	Source           string    `json:"source" gorm:"column:source;size:16;not null;default:manual"`
	EvidenceCount    int       `json:"evidence_count" gorm:"column:evidence_count;not null;default:0"` // 独立超限证据计数（学习用）
	UpdatedAt        time.Time `json:"updated_at" gorm:"column:updated_at"`
}

// TableName 显式指定表名，避免 GORM 复数化差异带来的迁移漂移。
func (ChannelModelLimit) TableName() string { return "channel_model_limits" }
