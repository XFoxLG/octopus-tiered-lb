package migrate

import (
	"fmt"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 69,
		Up:      migrateChannelCapabilityProbe,
	})
}

// 069: 能力探测的落库表 + 把旧的分组条目级 tools 结论搬到渠道×模型级。
//
// 三张表（建表本体由 AutoMigrate 完成，这里只保证已存在与数据平移）：
//   - channel_probe_runs：一次手动探测的元信息；
//   - channel_probe_results：逐行结果（协议层 + 能力层）；
//   - channel_model_capabilities：渠道×模型的能力结论。
//
// 数据平移：把 group_items 上的 SupportsTools* 三列聚合到
// channel_model_capabilities（能力 = tool_calling）。同一个渠道×模型可能出现在
// 多个分组里，若结论冲突则取"最新的 ProbedAt"，并在来源里标为 migrated，
// 让用户能区分"探测得来的"和"从旧结构搬来的"。
//
// 旧列**不在本迁移里删除**：先确认没有消费方，再单独一轮清理（与 068 的策略一致）。
func migrateChannelCapabilityProbe(database *gorm.DB) error {
	if database == nil {
		return fmt.Errorf("db is nil")
	}
	if !database.Migrator().HasTable(&model.GroupItem{}) {
		return nil
	}
	if !database.Migrator().HasTable(&model.ChannelModelCapability{}) {
		// 建表由 AutoMigrate 负责；此处拿不到表说明调用顺序异常，直接跳过数据平移。
		return nil
	}
	if !database.Migrator().HasColumn(&model.GroupItem{}, "SupportsTools") {
		return nil
	}
	return migrateGroupItemToolsCapability(database)
}

func migrateGroupItemToolsCapability(database *gorm.DB) error {
	type legacyToolsRow struct {
		ChannelID     int
		ModelName     string
		SupportsTools *bool
		ProbedAt      *int64
		ProbeKeyID    *int
	}
	rows := make([]legacyToolsRow, 0)
	if err := database.Table("group_items").
		Select("channel_id", "model_name", "supports_tools", "supports_tools_probed_at", "supports_tools_probe_key_id").
		Where("supports_tools IS NOT NULL").
		Scan(&rows).Error; err != nil {
		return fmt.Errorf("scan legacy group item tools verdicts: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	// 聚合到渠道×模型：结论冲突时保留 ProbedAt 更大的一条。
	type capabilityKey struct {
		channelID int
		modelName string
	}
	type capabilityValue struct {
		supported  bool
		probedAt   int64
		probeKeyID int
	}
	aggregated := make(map[capabilityKey]capabilityValue, len(rows))
	for _, row := range rows {
		if row.SupportsTools == nil {
			continue
		}
		key := capabilityKey{channelID: row.ChannelID, modelName: row.ModelName}
		probedAt := int64(0)
		if row.ProbedAt != nil {
			probedAt = *row.ProbedAt
		}
		probeKeyID := 0
		if row.ProbeKeyID != nil {
			probeKeyID = *row.ProbeKeyID
		}
		existing, ok := aggregated[key]
		if ok && existing.probedAt > probedAt {
			continue
		}
		aggregated[key] = capabilityValue{
			supported:  *row.SupportsTools,
			probedAt:   probedAt,
			probeKeyID: probeKeyID,
		}
	}

	for key, value := range aggregated {
		probedAt := time.Unix(value.probedAt, 0)
		if value.probedAt <= 0 {
			probedAt = time.Now()
		}
		record := model.ChannelModelCapability{
			ChannelID:  key.channelID,
			ModelName:  key.modelName,
			Capability: model.CapabilityToolCalling,
			Supported:  value.supported,
			Source:     "migrated",
			ProbeKeyID: value.probeKeyID,
			ProbedAt:   probedAt,
			UpdatedAt:  time.Now(),
		}
		// 已存在则跳过：迁移幂等，且不覆盖用户在新结构上重新探测过的结论。
		var existingCount int64
		if err := database.Model(&model.ChannelModelCapability{}).
			Where("channel_id = ? AND model_name = ? AND capability = ?", key.channelID, key.modelName, model.CapabilityToolCalling).
			Count(&existingCount).Error; err != nil {
			return fmt.Errorf("check existing capability: %w", err)
		}
		if existingCount > 0 {
			continue
		}
		if err := database.Create(&record).Error; err != nil {
			return fmt.Errorf("migrate tools capability for channel %d model %s: %w", key.channelID, key.modelName, err)
		}
	}
	return nil
}
