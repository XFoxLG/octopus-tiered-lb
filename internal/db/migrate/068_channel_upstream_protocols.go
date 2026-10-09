package migrate

import (
	"fmt"
	"strings"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 68,
		Up:      addChannelUpstreamProtocols,
	})
}

// 068: 把出站协议决策从分组下沉到渠道。
//
// 背景：分组 outbound_format 是分组级属性，但同一个分组里可能同时存在只支持
// Chat Completions 的公益站和原生 Gemini/Anthropic 渠道。协议必须能按渠道表达，
// 否则分组设了 passthrough/raw 会把原生渠道也塞进透传适配器（请求体形状不匹配，
// 直接打坏渠道）。
//
// 新增列：
//   - upstream_protocols: 渠道声明的协议有序列表（JSON），空 = 沿用分组；
//   - first_token_time_out / attempt_time_out / stream_idle_timeout:
//     渠道级超时覆盖，0 = 跟随分组（既有行默认 0，行为不变），
//     -1 = 显式关闭，>0 = 秒数；
//   - reasoning_buffer_strategy: 渠道级推理缓冲策略，空 = 沿用分组。
//
// 数据平移：把已有 channels.outbound_format_override（chat_only /
// responses_only）翻译成 upstream_protocols，保证升级后旧设置继续生效。
// 旧列保留不动（先 A 后 C：确认无依赖后再单独一轮删除）。
//
// 共库多实例部署的迁移互斥由 Migrate 入口的 Postgres advisory lock 保证。
func addChannelUpstreamProtocols(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.Channel{}) {
		return nil
	}

	columns := []struct {
		field string
		name  string
	}{
		{"UpstreamProtocols", "channels.upstream_protocols"},
		{"FirstTokenTimeOut", "channels.first_token_time_out"},
		{"AttemptTimeOut", "channels.attempt_time_out"},
		{"StreamIdleTimeout", "channels.stream_idle_timeout"},
		{"ReasoningBufferStrategy", "channels.reasoning_buffer_strategy"},
	}
	for _, column := range columns {
		if database.Migrator().HasColumn(&model.Channel{}, column.field) {
			continue
		}
		if err := database.Migrator().AddColumn(&model.Channel{}, column.field); err != nil {
			return fmt.Errorf("add %s: %w", column.name, err)
		}
	}

	// 超时三项的「未覆盖」值是 0，正好是 Go 零值与列默认值，所以不需要回填；
	// 只需把可能残留的 NULL 归一成 0，避免扫描进 int 时报错。
	for _, column := range []string{"first_token_time_out", "attempt_time_out", "stream_idle_timeout"} {
		if !database.Migrator().HasColumn(&model.Channel{}, column) {
			continue
		}
		if err := database.Exec("UPDATE channels SET " + column + " = 0 WHERE " + column + " IS NULL").Error; err != nil {
			return fmt.Errorf("normalize channels.%s: %w", column, err)
		}
	}

	if !database.Migrator().HasColumn(&model.Channel{}, "OutboundFormatOverride") {
		return nil
	}
	if !database.Migrator().HasColumn(&model.Channel{}, "UpstreamProtocols") {
		return nil
	}
	return backfillChannelUpstreamProtocols(database)
}

// backfillChannelUpstreamProtocols 把旧的 outbound_format_override 平移进新的
// upstream_protocols。只处理新列为空、旧列有值的行，因此：
//   - 幂等：重复执行不会覆盖用户已设置的新值；
//   - 非破坏：旧列原样保留，回滚时可继续读取。
func backfillChannelUpstreamProtocols(database *gorm.DB) error {
	type legacyRow struct {
		ID                     int
		OutboundFormatOverride string
		UpstreamProtocols      string
	}
	rows := make([]legacyRow, 0)
	if err := database.Table("channels").
		Select("id", "outbound_format_override", "upstream_protocols").
		Where("outbound_format_override IS NOT NULL AND outbound_format_override <> ''").
		Scan(&rows).Error; err != nil {
		return fmt.Errorf("scan legacy channel protocol overrides: %w", err)
	}
	for _, row := range rows {
		if strings.TrimSpace(row.UpstreamProtocols) != "" && strings.TrimSpace(row.UpstreamProtocols) != "null" && strings.TrimSpace(row.UpstreamProtocols) != "[]" {
			continue
		}
		translated, err := model.NormalizeUpstreamProtocols([]string{row.OutboundFormatOverride})
		if err != nil || len(translated) == 0 {
			// 未知的旧值无法翻译：保持旧列不动，运行时仍会按旧字段处理。
			continue
		}
		payload, err := model.MarshalUpstreamProtocols(translated)
		if err != nil {
			return fmt.Errorf("encode upstream protocols for channel %d: %w", row.ID, err)
		}
		if err := database.Table("channels").
			Where("id = ?", row.ID).
			Update("upstream_protocols", payload).Error; err != nil {
			return fmt.Errorf("backfill channel %d upstream protocols: %w", row.ID, err)
		}
	}
	return nil
}
