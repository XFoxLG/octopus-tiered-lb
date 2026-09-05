package model

// NotificationType identifies the source/category of an in-app notification.
type NotificationType string

const (
	NotificationTypeChannelExpire NotificationType = "channel_expire"
	NotificationTypeSystem        NotificationType = "system"
	NotificationTypeBackup        NotificationType = "backup"
	NotificationTypeKeyHealth     NotificationType = "key_health"
)

// NotificationSeverity controls sorting, filtering, and external routing priority.
type NotificationSeverity string

const (
	NotificationSeverityInfo     NotificationSeverity = "info"
	NotificationSeveritySuccess  NotificationSeverity = "success"
	NotificationSeverityWarning  NotificationSeverity = "warning"
	NotificationSeverityError    NotificationSeverity = "error"
	NotificationSeverityCritical NotificationSeverity = "critical"
)

// Notification is the unified in-app notification inbox item.
type Notification struct {
	ID       int64                `json:"id" gorm:"primaryKey"`
	Type     NotificationType     `json:"type" gorm:"size:32;index;not null"`
	Severity NotificationSeverity `json:"severity" gorm:"size:16;index;not null;default:'info'"`
	Title    string               `json:"title" gorm:"not null"`
	Content  string               `json:"content" gorm:"type:text"`
	// i18n 键化字段：前端按当前 UI 语言用 t(TitleKey, TitleArgs) 渲染。
	// 为空时回退到上面的 Title/Content 原文（历史通知零破坏）。
	// 新通知同时填充这两组：Title/Content 存英文回退串（供搜索/未来外部分发），
	// TitleKey/ContentKey + *Args 存键与参数（前端优先使用）。
	TitleKey     string `json:"title_key,omitempty" gorm:"size:128;index"`
	TitleArgs    string `json:"title_args,omitempty" gorm:"type:text"`
	ContentKey   string `json:"content_key,omitempty" gorm:"size:128"`
	ContentArgs  string `json:"content_args,omitempty" gorm:"type:text"`
	Source       string `json:"source,omitempty" gorm:"size:64;index"`
	SourceID     string `json:"source_id,omitempty" gorm:"size:128;index"`
	DedupeKey    string `json:"dedupe_key,omitempty" gorm:"size:255;index"`
	MetadataJSON string `json:"metadata_json,omitempty" gorm:"type:text"`
	Link         string `json:"link,omitempty" gorm:"size:512"`
	ReadAt       *int64 `json:"read_at,omitempty" gorm:"index"`
	ArchivedAt   *int64 `json:"archived_at,omitempty" gorm:"index"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime:milli;index"`
	UpdatedAt    int64  `json:"updated_at" gorm:"autoUpdateTime:milli"`
}
