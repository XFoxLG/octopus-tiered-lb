package model

import "time"

// GroupHealthStatus 分组健康检查快照终态（Seller 移植）。
type GroupHealthStatus string

const (
	GroupHealthStatusRunning GroupHealthStatus = "running"
	GroupHealthStatusSuccess GroupHealthStatus = "success"
	GroupHealthStatusPartial GroupHealthStatus = "partial"
	GroupHealthStatusFailed  GroupHealthStatus = "failed"
)

// GroupHealthAttemptStatus 单次候选拨测结果。
type GroupHealthAttemptStatus string

const (
	GroupHealthAttemptStatusSuccess GroupHealthAttemptStatus = "success"
	GroupHealthAttemptStatusFailed  GroupHealthAttemptStatus = "failed"
	GroupHealthAttemptStatusSkipped GroupHealthAttemptStatus = "skipped"
)

// GroupHealthProbeMode 拨测模式：standard 遇首个成功即停（failover 语义），full 测完所有候选。
type GroupHealthProbeMode string

const (
	GroupHealthProbeModeStandard GroupHealthProbeMode = "standard"
	GroupHealthProbeModeFull     GroupHealthProbeMode = "full"
)

// GroupHealthSnapshot 一次分组健康检查的快照（含各候选拨测明细）。
type GroupHealthSnapshot struct {
	ID                  int                  `json:"id" gorm:"primaryKey"`
	GroupID             int                  `json:"group_id" gorm:"index"`
	GroupName           string               `json:"group_name" gorm:"size:191;not null"`
	GroupMode           GroupMode            `json:"group_mode" gorm:"not null"`
	ProbeMode           GroupHealthProbeMode `json:"probe_mode" gorm:"size:16;not null;default:'standard'"`
	RequestModel        string               `json:"request_model" gorm:"size:191;not null"`
	Status              GroupHealthStatus    `json:"status" gorm:"size:16;index;not null"`
	StartedAt           time.Time            `json:"started_at" gorm:"index;not null"`
	FinishedAt          *time.Time           `json:"finished_at"`
	DurationMS          int64                `json:"duration_ms" gorm:"not null;default:0"`
	SuccessfulChannelID *int                 `json:"successful_channel_id"`
	Message             string               `json:"message"`
	Attempts            []GroupHealthAttempt `json:"attempts,omitempty" gorm:"foreignKey:SnapshotID"`
}

// GroupHealthAttempt 快照内单个候选的拨测记录。
type GroupHealthAttempt struct {
	ID           int                      `json:"id" gorm:"primaryKey"`
	SnapshotID   int                      `json:"snapshot_id" gorm:"index;not null"`
	GroupItemID  int                      `json:"group_item_id" gorm:"not null"`
	ChannelID    int                      `json:"channel_id" gorm:"not null"`
	ChannelName  string                   `json:"channel_name" gorm:"size:191;not null"`
	ChannelKeyID int                      `json:"channel_key_id" gorm:"not null;default:0"`
	KeyRemark    string                   `json:"key_remark" gorm:"size:191"`
	ModelName    string                   `json:"model_name" gorm:"size:191;not null"`
	Priority     int                      `json:"priority" gorm:"not null;default:0"`
	Weight       int                      `json:"weight" gorm:"not null;default:0"`
	Status       GroupHealthAttemptStatus `json:"status" gorm:"size:16;not null"`
	HTTPStatus   int                      `json:"http_status" gorm:"not null;default:0"`
	DurationMS   int64                    `json:"duration_ms" gorm:"not null;default:0"`
	ErrorMessage string                   `json:"error_message"`
}

// GroupHealthGroupView 分组与其最新健康快照的聚合视图。
type GroupHealthGroupView struct {
	GroupID   int                  `json:"group_id"`
	GroupName string               `json:"group_name"`
	GroupMode GroupMode            `json:"group_mode"`
	Latest    *GroupHealthSnapshot `json:"latest,omitempty"`
}
