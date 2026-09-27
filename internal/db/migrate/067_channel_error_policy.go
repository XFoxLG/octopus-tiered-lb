package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 67,
		Up:      addChannelErrorPolicy,
	})
}

// 067: 渠道级错误策略字段。上游公益站/私有网关对同一语义的错误码与文案各不相同,
// 渠道级白名单/黑名单比全局规则更准。
//   - retryable_status_codes: 命中则强制进入换 Key/换渠道重试
//   - retryable_keywords: 与状态码 OR,任一命中即重试
//   - non_retryable_status_codes: 命中则强制不重试(优先级最高)
//   - error_message_template: 仅改写最终呈现文案,支持 {upstream} 占位符
//
// 既有行保持空串(= 完全沿用现有行为),行为不变。共库多实例部署的迁移互斥由
// Migrate 入口的 Postgres advisory lock 保证。
func addChannelErrorPolicy(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.Channel{}) {
		return nil
	}
	for _, column := range []struct {
		field string
		name  string
	}{
		{"RetryableStatusCodes", "channels.retryable_status_codes"},
		{"RetryableKeywords", "channels.retryable_keywords"},
		{"NonRetryableStatusCodes", "channels.non_retryable_status_codes"},
		{"ErrorMessageTemplate", "channels.error_message_template"},
	} {
		if database.Migrator().HasColumn(&model.Channel{}, column.field) {
			continue
		}
		if err := database.Migrator().AddColumn(&model.Channel{}, column.field); err != nil {
			return fmt.Errorf("add %s: %w", column.name, err)
		}
	}
	return nil
}
