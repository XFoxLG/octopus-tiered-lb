package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 60,
		Up:      addChannelOutboundOverride,
	})
}

// 060: 为渠道增加出站协议覆盖（outbound_format_override），渠道值优先于分组
// outbound_format。供只支持单一协议的 OpenAI 兼容上游（如仅 /v1/chat/completions
// 的公益站）禁用 auto 回退。既有行保持空串（= 跟随分组），行为不变。
func addChannelOutboundOverride(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.Channel{}) {
		return nil
	}
	if !database.Migrator().HasColumn(&model.Channel{}, "OutboundFormatOverride") {
		if err := database.Migrator().AddColumn(&model.Channel{}, "OutboundFormatOverride"); err != nil {
			return fmt.Errorf("add channels.outbound_format_override: %w", err)
		}
	}
	return nil
}
