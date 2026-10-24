package migrate

import (
	"fmt"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 82,
		Up:      migrateChannelProbeProtocolMetadata,
	})
}

// 082: 给 channel_probe_results 增加协议与请求指纹元数据列（只增不删、全部可空）。
//
// 背景：旧结果只留下「协议/能力判据 + 接口 ID」，无法回答「这条结果当时打的是哪个
// 端点、用哪个 Key、发的是什么正文」。082 之后的新结果会带上实际协议、去掉凭据的
// 地址、Key 范围（下标与 Key ID，**不含 token**）、stream、请求预算与请求体指纹。
//
// 三条约定：
//   - 只加列，不改写历史行：存量行保持 NULL，界面如实显示「未记录」。绝不按渠道
//     当前默认协议回填——那会把旧结论伪造成「已验证该协议」。
//   - 幂等：AutoMigrate 通常已经建好列，重复执行不报错、不动数据。
//   - 方言安全：走 Migrator 而不是手写 SQL（PostgreSQL 不接受反引号，见 075）。
func migrateChannelProbeProtocolMetadata(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.ChannelProbeResult{}) {
		// 建表由 AutoMigrate 负责；拿不到表说明调用顺序异常，直接跳过。
		return nil
	}
	for _, field := range []string{
		"EndpointID",
		"Protocol",
		"EndpointURL",
		"KeyScope",
		"RequestStream",
		"RequestBudgetMS",
		"RequestFingerprint",
	} {
		if database.Migrator().HasColumn(&model.ChannelProbeResult{}, field) {
			continue
		}
		if err := database.Migrator().AddColumn(&model.ChannelProbeResult{}, field); err != nil {
			return fmt.Errorf("add channel_probe_results.%s: %w", field, err)
		}
	}
	return nil
}
