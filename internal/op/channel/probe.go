package channel

import (
	"context"
	"fmt"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"gorm.io/gorm"
)

// probeHistoryLimit 是探测历史列表的默认返回条数上限。
const probeHistoryLimit = 20

// SaveProbeRun 落盘一次探测的元信息与逐行结果。
//
// 草稿渠道（ID <= 0）不落库：用户在新建表单里试探时还没有渠道记录，
// 写进去会留下无主数据。
func SaveProbeRun(ctx context.Context, run *model.ChannelProbeRun) error {
	if run == nil {
		return fmt.Errorf("probe run is nil")
	}
	if run.ChannelID <= 0 {
		return nil
	}
	database := db.GetDB().WithContext(ctx)
	stored := *run
	results := append([]model.ChannelProbeResult(nil), run.Results...)
	stored.Results = nil
	err := database.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&stored).Error; err != nil {
			return fmt.Errorf("create probe run: %w", err)
		}
		if len(results) == 0 {
			return nil
		}
		for index := range results {
			results[index].RunID = stored.ID
			results[index].ChannelID = stored.ChannelID
		}
		if err := tx.Create(&results).Error; err != nil {
			return fmt.Errorf("create probe results: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	stored.Results = results
	*run = stored
	return nil
}

// GetProbeRun 读取一次探测（含逐行结果），供「应用」按钮使用。
func GetProbeRun(ctx context.Context, runID int64) (*model.ChannelProbeRun, error) {
	if runID <= 0 {
		return nil, fmt.Errorf("run_id is required")
	}
	database := db.GetDB().WithContext(ctx)
	var run model.ChannelProbeRun
	if err := database.First(&run, runID).Error; err != nil {
		return nil, fmt.Errorf("probe run not found: %w", err)
	}
	var results []model.ChannelProbeResult
	if err := database.Where("run_id = ?", runID).
		Order("kind ASC, id ASC").
		Find(&results).Error; err != nil {
		return nil, fmt.Errorf("load probe results: %w", err)
	}
	run.Results = results
	return &run, nil
}

// ListProbeRuns 返回某渠道最近的探测历史（不含逐行结果，列表只需概览）。
func ListProbeRuns(ctx context.Context, channelID int, limit int) ([]model.ChannelProbeRun, error) {
	if channelID <= 0 {
		return nil, fmt.Errorf("channel_id is required")
	}
	if limit <= 0 || limit > probeHistoryLimit {
		limit = probeHistoryLimit
	}
	var runs []model.ChannelProbeRun
	if err := db.GetDB().WithContext(ctx).
		Where("channel_id = ?", channelID).
		Order("id DESC").
		Limit(limit).
		Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

// MarkProbeRunApplied 标记一次探测已被应用（界面用来说明"这条结果已经写回配置"）。
func MarkProbeRunApplied(ctx context.Context, runID int64) error {
	if runID <= 0 {
		return nil
	}
	return db.GetDB().WithContext(ctx).
		Model(&model.ChannelProbeRun{}).
		Where("id = ?", runID).
		Updates(map[string]any{
			"applied":    true,
			"applied_at": time.Now(),
		}).Error
}

// UpsertCapability 写入或更新一条渠道×模型能力结论。
//
// 冲突目标固定为 (channel_id, model_name, capability)，与模型上的复合唯一索引一致；
// 已存在则覆盖结论与时间戳——最近一次探测结果应当取代旧结论。
func UpsertCapability(ctx context.Context, capability model.ChannelModelCapability) error {
	if capability.ChannelID <= 0 {
		return nil
	}
	if capability.ProbedAt.IsZero() {
		capability.ProbedAt = time.Now()
	}
	capability.UpdatedAt = time.Now()
	return db.GetDB().WithContext(ctx).
		Where("channel_id = ? AND model_name = ? AND capability = ?",
			capability.ChannelID, capability.ModelName, capability.Capability).
		Assign(map[string]any{
			"supported":    capability.Supported,
			"source":       capability.Source,
			"probe_key_id": capability.ProbeKeyID,
			"probed_at":    capability.ProbedAt,
			"updated_at":   capability.UpdatedAt,
		}).
		FirstOrCreate(&capability).Error
}

// ListCapabilities 返回某渠道的能力结论（可按模型过滤）。
func ListCapabilities(ctx context.Context, channelID int, modelName string) ([]model.ChannelModelCapability, error) {
	if channelID <= 0 {
		return nil, fmt.Errorf("channel_id is required")
	}
	query := db.GetDB().WithContext(ctx).Where("channel_id = ?", channelID)
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	var capabilities []model.ChannelModelCapability
	if err := query.Order("model_name ASC, capability ASC").Find(&capabilities).Error; err != nil {
		return nil, err
	}
	return capabilities, nil
}

// ApplyProbeProtocols 把探测通过的协议写进渠道声明（只加不减）。
//
// 与 helper.ApplyChannelProbeRun 的分工：helper 负责算出"该加哪些"，
// 这里负责落库，避免 helper 反向依赖 op 层造成循环。
func ApplyProbeProtocols(ctx context.Context, channelID int, protocols []string) ([]string, error) {
	if channelID <= 0 || len(protocols) == 0 {
		return nil, nil
	}
	current, ok := chCache.Get(channelID)
	if !ok {
		return nil, fmt.Errorf("channel not found")
	}
	existing := current.EffectiveUpstreamProtocols()
	merged := make([]string, 0, len(existing)+len(protocols))
	merged = append(merged, existing...)
	merged = append(merged, protocols...)
	normalized, err := model.NormalizeUpstreamProtocols(merged)
	if err != nil {
		return nil, err
	}
	existingSet := make(map[string]bool, len(existing))
	for _, protocol := range existing {
		existingSet[protocol] = true
	}
	added := make([]string, 0, len(normalized))
	for _, protocol := range normalized {
		if !existingSet[protocol] {
			added = append(added, protocol)
		}
	}
	if len(added) == 0 {
		return nil, nil
	}
	if err := db.GetDB().WithContext(ctx).
		Model(&model.Channel{}).
		Where("id = ?", channelID).
		Select("upstream_protocols").
		Updates(&model.Channel{UpstreamProtocols: normalized}).Error; err != nil {
		return nil, fmt.Errorf("update channel protocols: %w", err)
	}
	if err := RefreshCacheByID(channelID, ctx); err != nil {
		return added, fmt.Errorf("channel protocols saved but cache refresh failed: %w", err)
	}
	return added, nil
}
