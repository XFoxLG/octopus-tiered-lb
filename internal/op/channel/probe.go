package channel

import (
	"context"
	"fmt"
	"strings"
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

// CapabilityStaleScope describes evidence invalidated by a configuration
// change. All is used when the channel switches connection generation; the
// narrower fields preserve evidence for untouched endpoints and keys.
type CapabilityStaleScope struct {
	All         bool
	Legacy      bool
	EndpointIDs []string
	KeyIDs      []int
	Reason      string
}

// UpsertCapability 写入或更新一条能力证据。
//
// 冲突目标是稳定渠道身份、endpoint、实际协议、模型、能力与 Key 的复合范围。
// 同一渠道的不同接口或 Key 不再互相覆盖；最近一次针对同一范围的探测结果
// 取代旧结论，并把该范围重新标为 current。
func UpsertCapability(ctx context.Context, capability model.ChannelModelCapability) error {
	if capability.ChannelID <= 0 {
		return nil
	}
	now := time.Now()
	if capability.ProbedAt.IsZero() {
		capability.ProbedAt = now
	}
	capability.UpdatedAt = now
	capability.ChannelUID = model.CapabilityScopeUID(capability.ChannelUID, capability.ChannelID)
	if capability.ConfigurationFingerprint == "" {
		if current, ok := chCache.Get(capability.ChannelID); ok {
			capability.ConfigurationFingerprint = model.CapabilityConfigurationFingerprint(
				&current, capability.EndpointID, capability.Protocol,
			)
		}
	}
	capability.Stale = false
	capability.StaleAt = nil
	capability.StaleReason = ""
	return db.GetDB().WithContext(ctx).
		Where("channel_id = ? AND endpoint_id = ? AND protocol = ? AND model_name = ? AND capability = ? AND probe_key_id = ?",
			capability.ChannelID, capability.EndpointID, capability.Protocol,
			capability.ModelName, capability.Capability, capability.ProbeKeyID).
		Assign(map[string]any{
			"channel_uid":               capability.ChannelUID,
			"supported":                 capability.Supported,
			"source":                    capability.Source,
			"configuration_fingerprint": capability.ConfigurationFingerprint,
			"stale":                     false,
			"stale_at":                  nil,
			"stale_reason":              "",
			"probed_at":                 capability.ProbedAt,
			"updated_at":                capability.UpdatedAt,
		}).
		FirstOrCreate(&capability).Error
}

// MarkCapabilitiesStale invalidates evidence for changed transport settings or
// keys. Historical rows are retained; only their current-capability status
// changes. Callers inside a channel update should use markCapabilitiesStaleTx
// so the configuration write and invalidation commit atomically.
func MarkCapabilitiesStale(ctx context.Context, channelID int, scope CapabilityStaleScope) error {
	if channelID <= 0 {
		return nil
	}
	return markCapabilitiesStaleTx(db.GetDB().WithContext(ctx), channelID, scope, time.Now())
}

func markCapabilitiesStaleTx(tx *gorm.DB, channelID int, scope CapabilityStaleScope, now time.Time) error {
	if tx == nil || channelID <= 0 {
		return nil
	}
	if scope.Reason == "" {
		scope.Reason = "configuration_changed"
	}
	base := func() *gorm.DB {
		return tx.Model(&model.ChannelModelCapability{}).
			Where("channel_id = ? AND stale = ?", channelID, false)
	}
	update := func(query *gorm.DB) error {
		result := query.Updates(map[string]any{
			"stale":        true,
			"stale_at":     now,
			"stale_reason": scope.Reason,
		})
		if result.Error != nil {
			return result.Error
		}
		return nil
	}
	if scope.All {
		return update(base())
	}
	if scope.Legacy {
		if err := update(base().Where("endpoint_id = ''")); err != nil {
			return err
		}
	}
	for _, endpointID := range scope.EndpointIDs {
		if strings.TrimSpace(endpointID) == "" {
			continue
		}
		if err := update(base().Where("endpoint_id = ?", endpointID)); err != nil {
			return err
		}
	}
	if len(scope.KeyIDs) > 0 {
		if err := update(base().Where("probe_key_id IN ?", scope.KeyIDs)); err != nil {
			return err
		}
	}
	return nil
}

// ListCapabilities 返回某渠道当前有效的能力结论（可按模型过滤）。
// 历史证据可以显式包含；默认查询绝不把 stale 行显示为当前能力。
func ListCapabilities(ctx context.Context, channelID int, modelName string, includeStale bool) ([]model.ChannelModelCapability, error) {
	if channelID <= 0 {
		return nil, fmt.Errorf("channel_id is required")
	}
	query := db.GetDB().WithContext(ctx).Where("channel_id = ?", channelID)
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	var capabilities []model.ChannelModelCapability
	if err := query.Order("model_name ASC, endpoint_id ASC, protocol ASC, capability ASC, probe_key_id ASC").
		Find(&capabilities).Error; err != nil {
		return nil, err
	}
	channel, hasChannel := chCache.Get(channelID)
	now := time.Now()
	current := capabilities[:0]
	for index := range capabilities {
		row := capabilities[index]
		if !row.Stale && hasChannel && row.ConfigurationFingerprint != "" {
			fingerprint := model.CapabilityConfigurationFingerprint(&channel, row.EndpointID, row.Protocol)
			if fingerprint != "" && fingerprint != row.ConfigurationFingerprint {
				row.Stale = true
				row.StaleAt = &now
				row.StaleReason = "configuration_fingerprint_changed"
			}
		}
		if row.Stale && !includeStale {
			continue
		}
		current = append(current, row)
	}
	return current, nil
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
	if current.ConnectionConfig != nil {
		return nil, fmt.Errorf("connection_config_conflict: edit interfaces instead of legacy protocol declarations")
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
