package analytics

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/apikey"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/op/stats"
	"github.com/lingyuins/octopus/internal/relay/balancer"
	"gorm.io/gorm"
)

const analyticsRouteHealthFailureWindow = 24 * time.Hour

type analyticsSummaryRow struct {
	InputTokens    int64   `gorm:"column:input_tokens"`
	OutputTokens   int64   `gorm:"column:output_tokens"`
	TotalCost      float64 `gorm:"column:total_cost"`
	RequestSuccess int64   `gorm:"column:request_success"`
	RequestFailed  int64   `gorm:"column:request_failed"`
	RequestCount   int64   `gorm:"column:request_count"`
	FallbackCount  int64   `gorm:"column:fallback_count"`
}

type analyticsProviderAggregateRow struct {
	ChannelID      int     `gorm:"column:channel_id"`
	ChannelName    string  `gorm:"column:channel_name"`
	InputTokens    int64   `gorm:"column:input_tokens"`
	OutputTokens   int64   `gorm:"column:output_tokens"`
	TotalCost      float64 `gorm:"column:total_cost"`
	RequestSuccess int64   `gorm:"column:request_success"`
	RequestFailed  int64   `gorm:"column:request_failed"`
}

type analyticsModelAggregateRow struct {
	ModelName      string  `gorm:"column:model_name"`
	InputTokens    int64   `gorm:"column:input_tokens"`
	OutputTokens   int64   `gorm:"column:output_tokens"`
	TotalCost      float64 `gorm:"column:total_cost"`
	RequestSuccess int64   `gorm:"column:request_success"`
	RequestFailed  int64   `gorm:"column:request_failed"`
}

type analyticsAPIKeyAggregateRow struct {
	APIKeyID       int     `gorm:"column:api_key_id"`
	Name           string  `gorm:"column:name"`
	InputTokens    int64   `gorm:"column:input_tokens"`
	OutputTokens   int64   `gorm:"column:output_tokens"`
	TotalCost      float64 `gorm:"column:total_cost"`
	RequestSuccess int64   `gorm:"column:request_success"`
	RequestFailed  int64   `gorm:"column:request_failed"`
}

type analyticsChannelModelAggregateRow struct {
	ChannelID      int     `gorm:"column:channel_id"`
	ChannelName    string  `gorm:"column:channel_name"`
	ModelName      string  `gorm:"column:model_name"`
	InputTokens    int64   `gorm:"column:input_tokens"`
	OutputTokens   int64   `gorm:"column:output_tokens"`
	TotalCost      float64 `gorm:"column:total_cost"`
	RequestSuccess int64   `gorm:"column:request_success"`
	RequestFailed  int64   `gorm:"column:request_failed"`
}

type analyticsFailureAggregateRow struct {
	ChannelID        int
	RequestModelName string
	ActualModelName  string
	FailureCount     int64
	LastFailureAt    int64
}

func AnalyticsOverviewGet(ctx context.Context, r model.AnalyticsRange) (*model.AnalyticsOverview, error) {
	daily, err := stats.GetDaily(ctx)
	if err != nil {
		return nil, err
	}
	mergedDaily := mergeAnalyticsDailyWithToday(daily, stats.TodayGet())
	metrics := aggregateAnalyticsDailyMetrics(mergedDaily, r, stats.Now())

	// 活跃渠道/模型/API Key 取该时间段内 relay_logs 中实际出现过请求的去重数，
	// 而非启用渠道上配置的模型总数（后者会给出 57 渠道 / 2817 模型这类不真实数字）。
	activeChannels, activeModels, activeAPIKeys, err := loadAnalyticsActiveCounts(ctx, r)
	if err != nil {
		return nil, err
	}

	// 全量计数：启用渠道数、启用渠道配置的去重模型数、启用的 API Key 数。
	// 供前端在「活跃」与「全部」两种口径间切换展示（issue #145）。
	enabledChannels, enabledModels, enabledAPIKeys, err := loadAnalyticsEnabledCounts(ctx)
	if err != nil {
		return nil, err
	}

	logSummary, err := loadAnalyticsSummary(ctx, r)
	if err != nil {
		return nil, err
	}
	fallbackRate := 0.0
	if logSummary.RequestCount > 0 {
		fallbackRate = (float64(logSummary.FallbackCount) / float64(logSummary.RequestCount)) * 100
	}

	overview := buildAnalyticsOverview(
		metrics, activeChannels, activeAPIKeys, activeModels,
		enabledChannels, enabledModels, enabledAPIKeys,
		fallbackRate,
	)
	return &overview, nil
}

func AnalyticsUtilizationGet(ctx context.Context, r model.AnalyticsRange) (*model.AnalyticsUtilization, error) {
	providerBreakdown, err := AnalyticsProviderBreakdownGet(ctx, r)
	if err != nil {
		return nil, err
	}
	modelBreakdown, err := AnalyticsModelBreakdownGet(ctx, r)
	if err != nil {
		return nil, err
	}
	apiKeyBreakdown, err := AnalyticsAPIKeyBreakdownGet(ctx, r)
	if err != nil {
		return nil, err
	}

	return &model.AnalyticsUtilization{
		ProviderBreakdown: providerBreakdown,
		ModelBreakdown:    modelBreakdown,
		APIKeyBreakdown:   apiKeyBreakdown,
	}, nil
}

func AnalyticsProviderBreakdownGet(ctx context.Context, r model.AnalyticsRange) ([]model.AnalyticsProviderBreakdownItem, error) {
	rows, err := loadAnalyticsProviderRows(ctx, r)
	if err != nil {
		return nil, err
	}

	channels, err := channel.List(ctx)
	if err != nil {
		return nil, err
	}
	channelByID := make(map[int]model.Channel, len(channels))
	for _, ch := range channels {
		channelByID[ch.ID] = ch
	}

	return buildProviderBreakdown(rows, channelByID), nil
}

func AnalyticsModelBreakdownGet(ctx context.Context, r model.AnalyticsRange) ([]model.AnalyticsModelBreakdownItem, error) {
	rows, err := loadAnalyticsModelRows(ctx, r)
	if err != nil {
		return nil, err
	}
	return buildModelBreakdown(rows), nil
}

func AnalyticsAPIKeyBreakdownGet(ctx context.Context, r model.AnalyticsRange) ([]model.AnalyticsAPIKeyBreakdownItem, error) {
	rows, err := loadAnalyticsAPIKeyRows(ctx, r)
	if err != nil {
		return nil, err
	}
	return buildAPIKeyBreakdown(rows), nil
}

// AnalyticsChannelModelBreakdownGet 返回 (渠道,模型) 交叉维度的统计。
// 成功/失败基于单次尝试（relay_log_attempts）聚合，使"渠道A 失败→重试到B 成功"的请求中
// 渠道A 的失败也反映到 A 的成功率上（issue #67）。token/cost 按请求顶层渠道归属
// （与 attempts 表的 channel 维度一致时才计入）。groupID 非空时只返回该组包含的
// (渠道,模型) 组合。
func AnalyticsChannelModelBreakdownGet(ctx context.Context, r model.AnalyticsRange, groupID *int) ([]model.AnalyticsChannelModelItem, error) {
	channels, err := channel.List(ctx)
	if err != nil {
		return nil, err
	}
	channelByID := make(map[int]model.Channel, len(channels))
	for _, ch := range channels {
		channelByID[ch.ID] = ch
	}

	// 可选：按分组 scope 过滤 (channelID, modelName) 集合。
	// zen/空 ModelName 的组项无法预先确定实际模型名（取决于请求的 zen/<model>），
	// 故按 channel 维度做通配匹配，避免失败重试渠道被错误丢弃（issue #103）。
	scope := channelModelScope{precise: map[string]struct{}{}, wildcardChannels: map[int]struct{}{}}
	if groupID != nil {
		groups, err := group.GroupList(ctx)
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			if g.ID != *groupID {
				continue
			}
			for _, it := range g.Items {
				mn := strings.TrimSpace(it.ModelName)
				if mn == "" || strings.EqualFold(mn, "zen") {
					scope.wildcardChannels[it.ChannelID] = struct{}{}
					continue
				}
				scope.precise[strconv.Itoa(it.ChannelID)+"\x00"+mn] = struct{}{}
			}
		}
	}

	rows, err := loadAnalyticsChannelModelRows(ctx, r, scope)
	if err != nil {
		return nil, err
	}
	return buildChannelModelBreakdown(rows, channelByID), nil
}

// AnalyticsAutoStrategyGet 返回 Auto 策略运行态快照（滑动窗口内的成功率/样本数/延迟）。
// groupID 非空时按该组的 (channel_id, model_name) 精确过滤；为空时返回全部。
// 供"Auto 实时表现"展示（issue #67）。
//
// 注意：早期实现仅按 channel_id 过滤，当同一渠道跨多个分组（如 chat 组 + embeddings 组）
// 时会把其他组的模型泄漏进来（issue #87 Bug）。现改为按 (channel_id, model_name) 精确匹配。
func AnalyticsAutoStrategyGet(ctx context.Context, groupID *int) ([]model.AutoStrategySnapshotItem, error) {
	channels, err := channel.List(ctx)
	if err != nil {
		return nil, err
	}
	channelByID := make(map[int]model.Channel, len(channels))
	for _, ch := range channels {
		channelByID[ch.ID] = ch
	}

	// 全量快照（内存操作，开销很小），按需在下面精确过滤。
	snapshot := balancer.GetAutoStatsSnapshot(nil)
	minSamples := balancer.GetAutoStrategyMinSamples()

	var scope map[string]struct{}
	if groupID != nil {
		groups, err := group.GroupList(ctx)
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			if g.ID != *groupID {
				continue
			}
			scope = buildGroupAutoScope(g.Items)
			break
		}
	}

	filtered := filterAutoSnapshot(snapshot, scope)
	return buildAutoStrategyItems(filtered, channelByID, minSamples), nil
}

// buildGroupAutoScope 根据组 Items 构造 (channelID, normalizedModel) 集合，
// 用于精确过滤 Auto 快照。model 名归一化以匹配 balancer 内部存储的 key。
func buildGroupAutoScope(items []model.GroupItem) map[string]struct{} {
	scope := make(map[string]struct{}, len(items))
	for _, it := range items {
		key := strconv.Itoa(it.ChannelID) + "\x00" + normalizeAutoModelName(it.ModelName)
		scope[key] = struct{}{}
	}
	return scope
}

// normalizeAutoModelName 与 balancer.normalizeAutoStatsModelName 保持一致：
// lowercase + trim，用于匹配快照里的模型名。
func normalizeAutoModelName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// filterAutoSnapshot 按 scope 过滤快照。scope 为空时返回全量。
// key 格式: "channelID\x00normalizedModel"。
func filterAutoSnapshot(snapshot []balancer.AutoStatsSnapshotItem, scope map[string]struct{}) []balancer.AutoStatsSnapshotItem {
	if len(scope) == 0 {
		return snapshot
	}
	out := make([]balancer.AutoStatsSnapshotItem, 0, len(snapshot))
	for _, s := range snapshot {
		key := strconv.Itoa(s.ChannelID) + "\x00" + normalizeAutoModelName(s.ModelName)
		if _, ok := scope[key]; ok {
			out = append(out, s)
		}
	}
	return out
}

// buildAutoStrategyItems 把 balancer 快照转换为对外 model 并排序。
// 成功率低的优先（突出问题渠道），其次样本数、渠道名、模型名。
func buildAutoStrategyItems(snapshot []balancer.AutoStatsSnapshotItem, channelByID map[int]model.Channel, minSamples int) []model.AutoStrategySnapshotItem {
	items := make([]model.AutoStrategySnapshotItem, 0, len(snapshot))
	for _, s := range snapshot {
		var lastActive int64
		if !s.LastActiveAt.IsZero() {
			lastActive = s.LastActiveAt.Unix()
		}
		chName := ""
		enabled := false
		if c, ok := channelByID[s.ChannelID]; ok {
			chName = c.Name
			enabled = c.Enabled
		}
		items = append(items, model.AutoStrategySnapshotItem{
			ChannelID:     s.ChannelID,
			ChannelName:   chName,
			Enabled:       enabled,
			ModelName:     s.ModelName,
			SuccessRate:   s.SuccessRate * 100,
			SampleCount:   s.SampleCount,
			AvgLatencyMs:  s.AvgLatencyMs,
			LastActiveAt:  lastActive,
			MinSamplesMet: s.SampleCount >= minSamples,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].SuccessRate != items[j].SuccessRate {
			return items[i].SuccessRate < items[j].SuccessRate
		}
		if items[i].SampleCount != items[j].SampleCount {
			return items[i].SampleCount > items[j].SampleCount
		}
		if items[i].ChannelName != items[j].ChannelName {
			return items[i].ChannelName < items[j].ChannelName
		}
		return items[i].ModelName < items[j].ModelName
	})

	return items
}

func AnalyticsGroupHealthGet(ctx context.Context) ([]model.AnalyticsGroupHealthItem, error) {
	groups, err := group.GroupList(ctx)
	if err != nil {
		return nil, err
	}
	channels, err := channel.List(ctx)
	if err != nil {
		return nil, err
	}

	channelByID := make(map[int]model.Channel, len(channels))
	for _, ch := range channels {
		channelByID[ch.ID] = ch
		channelByID[ch.ID] = ch
	}

	failures, err := loadAnalyticsFailureRows(ctx, stats.Now().Add(-analyticsRouteHealthFailureWindow))
	if err != nil {
		return nil, err
	}

	// 拉取全量 Auto 策略快照（内存操作），交给 buildGroupHealth 按每个组的
	// (channel_id, model_name) 精确过滤后填入 AutoItems。避免前端按 channel_id
	// 客户端过滤导致跨组渠道的他组模型泄漏（issue #87 Bug 修复）。
	autoSnapshot := balancer.GetAutoStatsSnapshot(nil)
	minSamples := balancer.GetAutoStrategyMinSamples()

	return buildGroupHealth(groups, channelByID, failures, autoSnapshot, minSamples), nil
}

func mergeAnalyticsDailyWithToday(daily []model.StatsDaily, today model.StatsDaily) []model.StatsDaily {
	if today.Date == "" {
		return daily
	}

	merged := make([]model.StatsDaily, 0, len(daily)+1)
	replaced := false
	for _, item := range daily {
		if item.Date == today.Date {
			merged = append(merged, today)
			replaced = true
			continue
		}
		merged = append(merged, item)
	}
	if !replaced {
		merged = append(merged, today)
	}
	return merged
}

func aggregateAnalyticsDailyMetrics(daily []model.StatsDaily, r model.AnalyticsRange, now time.Time) model.StatsMetrics {
	startDate := analyticsStartDate(r, now)
	var metrics model.StatsMetrics
	for _, item := range daily {
		if startDate != "" && item.Date < startDate {
			continue
		}
		metrics.Add(item.StatsMetrics)
	}
	return metrics
}

func buildAnalyticsOverview(
	metrics model.StatsMetrics,
	providerCount, apiKeyCount, modelCount int,
	enabledProviderCount, enabledModelCount, enabledAPIKeyCount int,
	fallbackRate float64,
) model.AnalyticsOverview {
	requestCount := metrics.RequestSuccess + metrics.RequestFailed
	successRate := 0.0
	if requestCount > 0 {
		successRate = (float64(metrics.RequestSuccess) / float64(requestCount)) * 100
	}

	return model.AnalyticsOverview{
		AnalyticsMetrics: model.AnalyticsMetrics{
			RequestCount: requestCount,
			TotalTokens:  metrics.InputToken + metrics.OutputToken,
			InputTokens:  metrics.InputToken,
			OutputTokens: metrics.OutputToken,
			TotalCost:    metrics.InputCost + metrics.OutputCost,
			SuccessRate:  successRate,
		},
		ProviderCount:        providerCount,
		APIKeyCount:          apiKeyCount,
		ModelCount:           modelCount,
		FallbackRate:         fallbackRate,
		EnabledProviderCount: enabledProviderCount,
		EnabledModelCount:    enabledModelCount,
		EnabledAPIKeyCount:   enabledAPIKeyCount,
	}
}

func buildProviderBreakdown(rows map[int]*analyticsProviderAggregateRow, channelByID map[int]model.Channel) []model.AnalyticsProviderBreakdownItem {
	items := make([]model.AnalyticsProviderBreakdownItem, 0, len(rows))
	for channelID, row := range rows {
		if row == nil {
			continue
		}

		requestCount := row.RequestSuccess + row.RequestFailed
		successRate := 0.0
		if requestCount > 0 {
			successRate = (float64(row.RequestSuccess) / float64(requestCount)) * 100
		}

		channelName := strings.TrimSpace(row.ChannelName)
		enabled := false
		if c, ok := channelByID[channelID]; ok {
			if channelName == "" {
				channelName = c.Name
			}
			enabled = c.Enabled
		}
		if channelName == "" {
			channelName = "Unknown Channel"
		}

		items = append(items, model.AnalyticsProviderBreakdownItem{
			ChannelID:   channelID,
			ChannelName: channelName,
			Enabled:     enabled,
			AnalyticsMetrics: model.AnalyticsMetrics{
				RequestCount: requestCount,
				TotalTokens:  row.InputTokens + row.OutputTokens,
				InputTokens:  row.InputTokens,
				OutputTokens: row.OutputTokens,
				TotalCost:    row.TotalCost,
				SuccessRate:  successRate,
			},
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].RequestCount != items[j].RequestCount {
			return items[i].RequestCount > items[j].RequestCount
		}
		if items[i].TotalCost != items[j].TotalCost {
			return items[i].TotalCost > items[j].TotalCost
		}
		return items[i].ChannelName < items[j].ChannelName
	})

	return items
}

func buildModelBreakdown(rows map[string]*analyticsModelAggregateRow) []model.AnalyticsModelBreakdownItem {
	items := make([]model.AnalyticsModelBreakdownItem, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		modelName := strings.TrimSpace(row.ModelName)
		if modelName == "" {
			continue
		}

		requestCount := row.RequestSuccess + row.RequestFailed
		successRate := 0.0
		if requestCount > 0 {
			successRate = (float64(row.RequestSuccess) / float64(requestCount)) * 100
		}

		items = append(items, model.AnalyticsModelBreakdownItem{
			ModelName: modelName,
			AnalyticsMetrics: model.AnalyticsMetrics{
				RequestCount: requestCount,
				TotalTokens:  row.InputTokens + row.OutputTokens,
				InputTokens:  row.InputTokens,
				OutputTokens: row.OutputTokens,
				TotalCost:    row.TotalCost,
				SuccessRate:  successRate,
			},
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].RequestCount != items[j].RequestCount {
			return items[i].RequestCount > items[j].RequestCount
		}
		if items[i].TotalCost != items[j].TotalCost {
			return items[i].TotalCost > items[j].TotalCost
		}
		return items[i].ModelName < items[j].ModelName
	})

	return items
}

func buildAPIKeyBreakdown(rows map[string]*analyticsAPIKeyAggregateRow) []model.AnalyticsAPIKeyBreakdownItem {
	items := make([]model.AnalyticsAPIKeyBreakdownItem, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		name := strings.TrimSpace(row.Name)
		if name == "" {
			if row.APIKeyID > 0 {
				name = "Key #" + strconv.Itoa(row.APIKeyID)
			} else {
				name = "Unknown Key"
			}
		}

		requestCount := row.RequestSuccess + row.RequestFailed
		successRate := 0.0
		if requestCount > 0 {
			successRate = (float64(row.RequestSuccess) / float64(requestCount)) * 100
		}

		item := model.AnalyticsAPIKeyBreakdownItem{
			Name: name,
			AnalyticsMetrics: model.AnalyticsMetrics{
				RequestCount: requestCount,
				TotalTokens:  row.InputTokens + row.OutputTokens,
				InputTokens:  row.InputTokens,
				OutputTokens: row.OutputTokens,
				TotalCost:    row.TotalCost,
				SuccessRate:  successRate,
			},
		}
		if row.APIKeyID > 0 {
			id := row.APIKeyID
			item.APIKeyID = &id
		}
		items = append(items, item)
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].RequestCount != items[j].RequestCount {
			return items[i].RequestCount > items[j].RequestCount
		}
		if items[i].TotalCost != items[j].TotalCost {
			return items[i].TotalCost > items[j].TotalCost
		}
		return items[i].Name < items[j].Name
	})

	return items
}

func buildChannelModelBreakdown(rows map[string]*analyticsChannelModelAggregateRow, channelByID map[int]model.Channel) []model.AnalyticsChannelModelItem {
	items := make([]model.AnalyticsChannelModelItem, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		channelName := strings.TrimSpace(row.ChannelName)
		enabled := false
		if c, ok := channelByID[row.ChannelID]; ok {
			if channelName == "" {
				channelName = c.Name
			}
			enabled = c.Enabled
		}
		if channelName == "" {
			channelName = "Unknown Channel"
		}

		requestCount := row.RequestSuccess + row.RequestFailed
		successRate := 0.0
		if requestCount > 0 {
			successRate = (float64(row.RequestSuccess) / float64(requestCount)) * 100
		}

		items = append(items, model.AnalyticsChannelModelItem{
			ChannelID:   row.ChannelID,
			ChannelName: channelName,
			ModelName:   row.ModelName,
			Enabled:     enabled,
			AnalyticsMetrics: model.AnalyticsMetrics{
				RequestCount: requestCount,
				TotalTokens:  row.InputTokens + row.OutputTokens,
				InputTokens:  row.InputTokens,
				OutputTokens: row.OutputTokens,
				TotalCost:    row.TotalCost,
				SuccessRate:  successRate,
			},
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		// 失败数大的优先（突出问题渠道），其次请求数、费用、名称。
		// 失败数 = round(RequestCount * (1 - SuccessRate/100))。
		ifailed := int64(float64(items[i].RequestCount) * (1 - items[i].SuccessRate/100))
		jfailed := int64(float64(items[j].RequestCount) * (1 - items[j].SuccessRate/100))
		if ifailed != jfailed {
			return ifailed > jfailed
		}
		if items[i].RequestCount != items[j].RequestCount {
			return items[i].RequestCount > items[j].RequestCount
		}
		if items[i].ChannelName != items[j].ChannelName {
			return items[i].ChannelName < items[j].ChannelName
		}
		return items[i].ModelName < items[j].ModelName
	})

	return items
}

func buildGroupHealth(groups []model.Group, channelByID map[int]model.Channel, failures map[string]*analyticsFailureAggregateRow, autoSnapshot []balancer.AutoStatsSnapshotItem, minSamples int) []model.AnalyticsGroupHealthItem {
	items := make([]model.AnalyticsGroupHealthItem, 0, len(groups))
	for _, group := range groups {
		itemCount := len(group.Items)
		enabledItemCount := 0
		disabledItemCount := 0
		failureCount := int64(0)
		lastFailureAt := int64(0)

		// 按 (channelID,modelName) 聚合失败，供下钻展示。
		type chanFail struct {
			ChannelID     int
			ChannelName   string
			ModelName     string
			FailureCount  int64
			LastFailureAt int64
		}
		chanFailures := make(map[string]*chanFail)
		seenFailureKeys := make(map[string]struct{})
		for _, item := range group.Items {
			c, ok := channelByID[item.ChannelID]
			if ok && c.Enabled {
				enabledItemCount++
			} else {
				disabledItemCount++
			}

			for _, key := range []string{
				makeAnalyticsFailureKey(item.ChannelID, item.ModelName, item.ModelName),
				makeAnalyticsFailureKey(item.ChannelID, item.ModelName, group.Name),
			} {
				if _, ok := seenFailureKeys[key]; ok {
					continue
				}
				seenFailureKeys[key] = struct{}{}
				failure, ok := failures[key]
				if !ok || failure == nil {
					continue
				}
				failureCount += failure.FailureCount
				if failure.LastFailureAt > lastFailureAt {
					lastFailureAt = failure.LastFailureAt
				}

				// 按 (channelID, model) 聚合到下钻 map。model 优先用 attempt 的 actual，
				// 没有则用 item.ModelName。
				cfKey := strconv.Itoa(item.ChannelID) + "\x00" + item.ModelName
				cf, ok := chanFailures[cfKey]
				if !ok {
					cf = &chanFail{
						ChannelID: item.ChannelID,
						ModelName: item.ModelName,
					}
					if c, ok := channelByID[item.ChannelID]; ok {
						cf.ChannelName = c.Name
					}
					chanFailures[cfKey] = cf
				}
				cf.FailureCount += failure.FailureCount
				if failure.LastFailureAt > cf.LastFailureAt {
					cf.LastFailureAt = failure.LastFailureAt
				}
			}
		}

		status := "healthy"
		score := 100
		switch {
		case itemCount == 0:
			status = "empty"
			score = 0
		case enabledItemCount == 0:
			status = "down"
			score = 20
		default:
			score -= (disabledItemCount * 40) / itemCount
			if failureCount > 0 {
				penalty := int(failureCount * 12)
				if penalty > 48 {
					penalty = 48
				}
				score -= penalty
			}
			if disabledItemCount > 0 || failureCount >= 3 {
				status = "degraded"
			} else if failureCount > 0 {
				status = "warning"
			}
		}

		if score < 0 {
			score = 0
		}

		// 仅保留有失败的渠道，按失败数降序，取前 10 供下钻展示。
		failingChannels := make([]model.FailingChannelItem, 0, len(chanFailures))
		for _, cf := range chanFailures {
			if cf.FailureCount <= 0 {
				continue
			}
			failingChannels = append(failingChannels, model.FailingChannelItem{
				ChannelID:     cf.ChannelID,
				ChannelName:   cf.ChannelName,
				ModelName:     cf.ModelName,
				FailureCount:  cf.FailureCount,
				LastFailureAt: cf.LastFailureAt,
			})
		}
		sort.SliceStable(failingChannels, func(i, j int) bool {
			if failingChannels[i].FailureCount != failingChannels[j].FailureCount {
				return failingChannels[i].FailureCount > failingChannels[j].FailureCount
			}
			return failingChannels[i].ChannelName < failingChannels[j].ChannelName
		})
		if len(failingChannels) > 10 {
			failingChannels = failingChannels[:10]
		}

		// 收集该组涉及的所有渠道 ID，供前端按组过滤 Auto 策略表现。
		channelIDs := make([]int, 0, len(group.Items))
		seenChannels := make(map[int]struct{})
		for _, item := range group.Items {
			if _, ok := seenChannels[item.ChannelID]; ok {
				continue
			}
			seenChannels[item.ChannelID] = struct{}{}
			channelIDs = append(channelIDs, item.ChannelID)
		}

		// 仅 Auto 组（mode==5）按本组 (channel_id, model_name) 精确过滤 Auto 快照，
		// 填入 AutoItems 供前端直接展示。取前 12 条（与前端原逻辑一致）。
		// 这里按 (channel, model) 精确匹配，避免跨组渠道的他组模型泄漏（issue #87 Bug）。
		var autoItems []model.AutoStrategySnapshotItem
		if group.Mode == model.GroupModeAuto {
			scope := buildGroupAutoScope(group.Items)
			filtered := filterAutoSnapshot(autoSnapshot, scope)
			autoItems = buildAutoStrategyItems(filtered, channelByID, minSamples)
			if len(autoItems) > 12 {
				autoItems = autoItems[:12]
			}
		}

		items = append(items, model.AnalyticsGroupHealthItem{
			GroupID:           group.ID,
			GroupName:         group.Name,
			EndpointType:      group.EndpointType,
			ItemCount:         itemCount,
			EnabledItemCount:  enabledItemCount,
			DisabledItemCount: disabledItemCount,
			FailureCount:      failureCount,
			LastFailureAt:     lastFailureAt,
			HealthScore:       score,
			Status:            status,
			Mode:              int(group.Mode),
			FailingChannels:   failingChannels,
			ChannelIDs:        channelIDs,
			AutoItems:         autoItems,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].HealthScore != items[j].HealthScore {
			return items[i].HealthScore < items[j].HealthScore
		}
		if items[i].FailureCount != items[j].FailureCount {
			return items[i].FailureCount > items[j].FailureCount
		}
		return items[i].GroupName < items[j].GroupName
	})

	return items
}

func loadAnalyticsSummary(ctx context.Context, r model.AnalyticsRange) (*analyticsSummaryRow, error) {
	startUnix := analyticsRangeStartUnix(r, stats.Now())
	row := &analyticsSummaryRow{}

	keepEnabled, err := setting.GetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return nil, err
	}

	if keepEnabled {
		query := relayLogReadConn().WithContext(ctx).Model(&model.RelayLog{}).
			Select(`
				COUNT(*) AS request_count,
				COALESCE(SUM(CASE WHEN total_attempts > 1 THEN 1 ELSE 0 END), 0) AS fallback_count
			`)
		if startUnix != nil {
			query = query.Where("time >= ?", *startUnix)
		}
		if err := query.Scan(row).Error; err != nil {
			return nil, err
		}
	}

	cache, lock := relaylog.GetCacheAndLock()
	lock.Lock()
	for _, logItem := range cache {
		if startUnix != nil && logItem.Time < *startUnix {
			continue
		}
		row.RequestCount++
		if logItem.TotalAttempts > 1 {
			row.FallbackCount++
		}
	}
	lock.Unlock()

	return row, nil
}

// loadAnalyticsActiveCounts 统计某时间范围内实际出现过请求的去重渠道/模型/API Key 数。
// 与 loadAnalyticsSummary 同样的双源模式：keepEnabled 时查 DB（LogDB 优先）拿到
// distinct 集合，再无条件合并内存缓存（最近 200 条尚未落库的日志）。模型名沿用
// actual->request 回退约定（与 loadAnalyticsModelRowsFromRelayLogs 一致）。
// API Key 跳过 0（匿名请求不计入活跃 API Key）。
func loadAnalyticsActiveCounts(ctx context.Context, r model.AnalyticsRange) (int, int, int, error) {
	startUnix := analyticsRangeStartUnix(r, stats.Now())

	channelSet := make(map[int]struct{})
	modelSet := make(map[string]struct{})
	apiKeySet := make(map[int]struct{})

	keepEnabled, err := setting.GetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return 0, 0, 0, err
	}
	if keepEnabled {
		// 渠道
		var chIDs []int
		chQ := relayLogReadConn().WithContext(ctx).Model(&model.RelayLog{}).
			Distinct("channel_id")
		if startUnix != nil {
			chQ = chQ.Where("time >= ?", *startUnix)
		}
		if err := chQ.Pluck("channel_id", &chIDs).Error; err != nil {
			return 0, 0, 0, err
		}
		for _, id := range chIDs {
			channelSet[id] = struct{}{}
		}

		// 模型（actual_model_name 为空时回退到 request_model_name）
		var modelNames []string
		mQ := relayLogReadConn().WithContext(ctx).Model(&model.RelayLog{}).
			Distinct("COALESCE(NULLIF(actual_model_name, ''), request_model_name)")
		if startUnix != nil {
			mQ = mQ.Where("time >= ?", *startUnix)
		}
		if err := mQ.Pluck("COALESCE(NULLIF(actual_model_name, ''), request_model_name)", &modelNames).Error; err != nil {
			return 0, 0, 0, err
		}
		for _, mn := range modelNames {
			mn = strings.TrimSpace(mn)
			if mn != "" {
				modelSet[mn] = struct{}{}
			}
		}

		// API Key（跳过 0=匿名）
		var keyIDs []int
		kQ := relayLogReadConn().WithContext(ctx).Model(&model.RelayLog{}).
			Distinct("request_api_key_id").
			Where("request_api_key_id > 0")
		if startUnix != nil {
			kQ = kQ.Where("time >= ?", *startUnix)
		}
		if err := kQ.Pluck("request_api_key_id", &keyIDs).Error; err != nil {
			return 0, 0, 0, err
		}
		for _, id := range keyIDs {
			apiKeySet[id] = struct{}{}
		}
	}

	// 内存缓存（最近未落库的日志）无条件合并
	cache, lock := relaylog.GetCacheAndLock()
	lock.Lock()
	for _, logItem := range cache {
		if startUnix != nil && logItem.Time < *startUnix {
			continue
		}
		channelSet[logItem.ChannelId] = struct{}{}
		if logItem.RequestAPIKeyID > 0 {
			apiKeySet[logItem.RequestAPIKeyID] = struct{}{}
		}
		modelName := strings.TrimSpace(logItem.ActualModelName)
		if modelName == "" {
			modelName = strings.TrimSpace(logItem.RequestModelName)
		}
		if modelName != "" {
			modelSet[modelName] = struct{}{}
		}
	}
	lock.Unlock()

	return len(channelSet), len(modelSet), len(apiKeySet), nil
}

// loadAnalyticsEnabledCounts 统计当前启用状态的渠道/模型/API Key 全量计数。
// 与 loadAnalyticsActiveCounts（按时间范围统计实际有请求的去重数）互补，
// 供前端在「活跃」与「全部」口径间切换（issue #145）。
func loadAnalyticsEnabledCounts(ctx context.Context) (int, int, int, error) {
	channels, err := channel.List(ctx)
	if err != nil {
		return 0, 0, 0, err
	}

	providerCount := 0
	modelNames := make(map[string]struct{})
	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}
		providerCount++
		for _, modelName := range splitAnalyticsChannelModels(ch) {
			modelNames[modelName] = struct{}{}
		}
	}

	apiKeys, err := apikey.List(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	apiKeyCount := 0
	for _, apiKey := range apiKeys {
		if apiKey.Enabled {
			apiKeyCount++
		}
	}

	return providerCount, len(modelNames), apiKeyCount, nil
}

// channelModelScope 描述分组维度下的 (渠道,模型) 过滤集合。
// precise 为精确匹配的 "channelID\x00modelName" 集合；wildcardChannels 收集
// zen/空 ModelName 的组项所在渠道——这些渠道的实际模型名取决于请求的
// zen/<model> 前缀，无法预先确定，故按 channel 维度通配（issue #103）。
type channelModelScope struct {
	precise          map[string]struct{}
	wildcardChannels map[int]struct{}
}

// inScope 报告 (channelID, modelName) 是否落在 scope 内。scope 为空（未按分组
// 过滤）时全部保留。
func (s channelModelScope) inScope(channelID int, modelName string) bool {
	if len(s.precise) == 0 && len(s.wildcardChannels) == 0 {
		return true
	}
	if _, ok := s.precise[strconv.Itoa(channelID)+"\x00"+modelName]; ok {
		return true
	}
	if _, ok := s.wildcardChannels[channelID]; ok {
		return true
	}
	return false
}

// loadAnalyticsChannelModelRows 聚合 (渠道,模型) 维度的成功/失败/token/cost。
// 成功/失败按单次尝试（relay_log_attempts）统计；token/cost 取自 relay_logs 且仅在
// 该请求最终成功时计入（避免把整体失败的请求 token 重复计入多个渠道）。
// scope 非空时只保留其中的 (channelID,modelName) 组合；zen/空 ModelName 的组项
// 按 channel 维度通配（issue #103）。
func loadAnalyticsChannelModelRows(ctx context.Context, r model.AnalyticsRange, scope channelModelScope) (map[string]*analyticsChannelModelAggregateRow, error) {
	startDate := analyticsStartDate(r, stats.Now())
	rows := make(map[string]*analyticsChannelModelAggregateRow)
	inScope := scope.inScope

	conn := db.GetDB()
	if conn == nil {
		return loadAnalyticsChannelModelRowsFromRelayLogs(ctx, r, scope)
	}
	var dbRows []analyticsChannelModelAggregateRow
	query := conn.WithContext(ctx).Model(&model.StatsDailyChannelModel{}).
		Select(`
			channel_id,
			MAX(channel_name) AS channel_name,
			model_name,
			COALESCE(SUM(input_token), 0) AS input_tokens,
			COALESCE(SUM(output_token), 0) AS output_tokens,
			COALESCE(SUM(input_cost + output_cost), 0) AS total_cost,
			COALESCE(SUM(request_success), 0) AS request_success,
			COALESCE(SUM(request_failed), 0) AS request_failed
		`).
		Group("channel_id, model_name")
	if startDate != "" {
		query = query.Where("date >= ?", startDate)
	}
	if err := query.Scan(&dbRows).Error; err != nil {
		return loadAnalyticsChannelModelRowsFromRelayLogs(ctx, r, scope)
	}
	for _, row := range dbRows {
		modelName := strings.TrimSpace(row.ModelName)
		if modelName == "" || !inScope(row.ChannelID, modelName) {
			continue
		}
		rowCopy := row
		rowCopy.ModelName = modelName
		key := strconv.Itoa(row.ChannelID) + "\x00" + modelName
		rows[key] = &rowCopy
	}
	// stats_daily 为主、relay_logs 补缺：stats_daily 未覆盖的 (channel, model)
	// 组合从 relay_logs 补充，避免迁移后旧数据消失（issue #148）。
	// 仅补缺不合并，避免同一请求在两个数据源中双计。
	logRows, logErr := loadAnalyticsChannelModelRowsFromRelayLogs(ctx, r, scope)
	if logErr == nil {
		for key, logRow := range logRows {
			if _, exists := rows[key]; !exists {
				rows[key] = logRow
			}
		}
	}

	return rows, nil
}

func mergeRelayLogIntoChannelModelRows(rows map[string]*analyticsChannelModelAggregateRow, log *model.RelayLog, inScope func(int, string) bool) {
	success := log.Error == ""
	if len(log.Attempts) > 0 {
		for _, a := range log.Attempts {
			if a.ChannelID == 0 {
				continue
			}
			modelName := strings.TrimSpace(a.ModelName)
			if modelName == "" {
				modelName = strings.TrimSpace(log.ActualModelName)
			}
			if modelName == "" {
				modelName = strings.TrimSpace(log.RequestModelName)
			}
			if !inScope(a.ChannelID, modelName) {
				continue
			}
			key := strconv.Itoa(a.ChannelID) + "\x00" + modelName
			row, ok := rows[key]
			if !ok {
				row = &analyticsChannelModelAggregateRow{ChannelID: a.ChannelID, ModelName: modelName}
				rows[key] = row
			}
			if a.Status == model.AttemptFailed {
				row.RequestFailed++
				continue
			}
			if a.Status == model.AttemptSuccess {
				row.RequestSuccess++
				// token/cost 仅在整体成功时计入该渠道（避免重复计入）。
				if success {
					row.InputTokens += int64(log.InputTokens)
					row.OutputTokens += int64(log.OutputTokens)
					row.TotalCost += log.Cost
				}
			}
			if row.ChannelName == "" {
				row.ChannelName = a.ChannelName
			}
		}
		return
	}
	// 回退：无尝试记录的历史日志，用顶层列（只反映最终渠道成败）。
	modelName := strings.TrimSpace(log.ActualModelName)
	if modelName == "" {
		modelName = strings.TrimSpace(log.RequestModelName)
	}
	if modelName == "" || log.ChannelId == 0 || !inScope(log.ChannelId, modelName) {
		return
	}
	key := strconv.Itoa(log.ChannelId) + "\x00" + modelName
	row, ok := rows[key]
	if !ok {
		row = &analyticsChannelModelAggregateRow{ChannelID: log.ChannelId, ModelName: modelName}
		rows[key] = row
	}
	if success {
		row.RequestSuccess++
		row.InputTokens += int64(log.InputTokens)
		row.OutputTokens += int64(log.OutputTokens)
		row.TotalCost += log.Cost
	} else {
		row.RequestFailed++
	}
	if row.ChannelName == "" {
		row.ChannelName = log.ChannelName
	}
}

func loadAnalyticsProviderRows(ctx context.Context, r model.AnalyticsRange) (map[int]*analyticsProviderAggregateRow, error) {
	startDate := analyticsStartDate(r, stats.Now())
	rows := make(map[int]*analyticsProviderAggregateRow)

	conn := db.GetDB()
	if conn == nil {
		return loadAnalyticsProviderRowsFromRelayLogs(ctx, r)
	}
	var dbRows []analyticsProviderAggregateRow
	query := conn.WithContext(ctx).Model(&model.StatsDailyChannel{}).
		Select(`
			channel_id,
			MAX(channel_name) AS channel_name,
			COALESCE(SUM(input_token), 0) AS input_tokens,
			COALESCE(SUM(output_token), 0) AS output_tokens,
			COALESCE(SUM(input_cost + output_cost), 0) AS total_cost,
			COALESCE(SUM(request_success), 0) AS request_success,
			COALESCE(SUM(request_failed), 0) AS request_failed
		`).
		Group("channel_id")
	if startDate != "" {
		query = query.Where("date >= ?", startDate)
	}
	if err := query.Scan(&dbRows).Error; err != nil {
		return loadAnalyticsProviderRowsFromRelayLogs(ctx, r)
	}
	for _, row := range dbRows {
		rowCopy := row
		rows[row.ChannelID] = &rowCopy
	}
	// stats_daily 为主、relay_logs 补缺（issue #148）。
	logRows, logErr := loadAnalyticsProviderRowsFromRelayLogs(ctx, r)
	if logErr == nil {
		for channelID, logRow := range logRows {
			if _, exists := rows[channelID]; !exists {
				rows[channelID] = logRow
			}
		}
	}

	return rows, nil
}

func loadAnalyticsModelRows(ctx context.Context, r model.AnalyticsRange) (map[string]*analyticsModelAggregateRow, error) {
	startDate := analyticsStartDate(r, stats.Now())
	rows := make(map[string]*analyticsModelAggregateRow)

	conn := db.GetDB()
	if conn == nil {
		return loadAnalyticsModelRowsFromRelayLogs(ctx, r)
	}
	var dbRows []analyticsModelAggregateRow
	query := conn.WithContext(ctx).Model(&model.StatsDailyModel{}).
		Select(`
			model_name,
			COALESCE(SUM(input_token), 0) AS input_tokens,
			COALESCE(SUM(output_token), 0) AS output_tokens,
			COALESCE(SUM(input_cost + output_cost), 0) AS total_cost,
			COALESCE(SUM(request_success), 0) AS request_success,
			COALESCE(SUM(request_failed), 0) AS request_failed
		`).
		Group("model_name")
	if startDate != "" {
		query = query.Where("date >= ?", startDate)
	}
	if err := query.Scan(&dbRows).Error; err != nil {
		return loadAnalyticsModelRowsFromRelayLogs(ctx, r)
	}
	for _, row := range dbRows {
		modelName := strings.TrimSpace(row.ModelName)
		if modelName == "" {
			continue
		}
		rowCopy := row
		rowCopy.ModelName = modelName
		rows[modelName] = &rowCopy
	}
	// stats_daily 为主、relay_logs 补缺（issue #148）。
	logRows, logErr := loadAnalyticsModelRowsFromRelayLogs(ctx, r)
	if logErr == nil {
		for modelName, logRow := range logRows {
			if _, exists := rows[modelName]; !exists {
				rows[modelName] = logRow
			}
		}
	}

	return rows, nil
}

func loadAnalyticsAPIKeyRows(ctx context.Context, r model.AnalyticsRange) (map[string]*analyticsAPIKeyAggregateRow, error) {
	startDate := analyticsStartDate(r, stats.Now())
	rows := make(map[string]*analyticsAPIKeyAggregateRow)

	conn := db.GetDB()
	if conn == nil {
		return loadAnalyticsAPIKeyRowsFromRelayLogs(ctx, r)
	}
	var dbRows []analyticsAPIKeyAggregateRow
	query := conn.WithContext(ctx).Model(&model.StatsDailyAPIKey{}).
		Select(`
			api_key_id,
			MAX(name) AS name,
			COALESCE(SUM(input_token), 0) AS input_tokens,
			COALESCE(SUM(output_token), 0) AS output_tokens,
			COALESCE(SUM(input_cost + output_cost), 0) AS total_cost,
			COALESCE(SUM(request_success), 0) AS request_success,
			COALESCE(SUM(request_failed), 0) AS request_failed
		`).
		Group("api_key_id")
	if startDate != "" {
		query = query.Where("date >= ?", startDate)
	}
	if err := query.Scan(&dbRows).Error; err != nil {
		return loadAnalyticsAPIKeyRowsFromRelayLogs(ctx, r)
	}
	for _, row := range dbRows {
		rowCopy := row
		rowCopy.Name = strings.TrimSpace(row.Name)
		rows[makeAnalyticsAPIKeyAggregateKey(row.APIKeyID, rowCopy.Name)] = &rowCopy
	}
	// stats_daily 为主、relay_logs 补缺（issue #148）。
	logRows, logErr := loadAnalyticsAPIKeyRowsFromRelayLogs(ctx, r)
	if logErr == nil {
		for key, logRow := range logRows {
			if _, exists := rows[key]; !exists {
				rows[key] = logRow
			}
		}
	}

	return rows, nil
}

func loadAnalyticsProviderRowsFromRelayLogs(ctx context.Context, r model.AnalyticsRange) (map[int]*analyticsProviderAggregateRow, error) {
	startUnix := analyticsRangeStartUnix(r, stats.Now())
	rows := make(map[int]*analyticsProviderAggregateRow)
	keepEnabled, err := setting.GetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return nil, err
	}
	if keepEnabled {
		var dbRows []analyticsProviderAggregateRow
		query := relayLogReadConn().WithContext(ctx).Model(&model.RelayLog{}).
			Select(`channel_id, channel_name, COALESCE(SUM(input_tokens), 0) AS input_tokens, COALESCE(SUM(output_tokens), 0) AS output_tokens, COALESCE(SUM(cost), 0) AS total_cost, COALESCE(SUM(CASE WHEN error = '' THEN 1 ELSE 0 END), 0) AS request_success, COALESCE(SUM(CASE WHEN error <> '' THEN 1 ELSE 0 END), 0) AS request_failed`).
			Group("channel_id, channel_name")
		if startUnix != nil {
			query = query.Where("time >= ?", *startUnix)
		}
		if err := query.Scan(&dbRows).Error; err != nil {
			return nil, err
		}
		for _, row := range dbRows {
			rowCopy := row
			rows[row.ChannelID] = &rowCopy
		}
	}
	cache, lock := relaylog.GetCacheAndLock()
	lock.Lock()
	defer lock.Unlock()
	for _, logItem := range cache {
		if startUnix != nil && logItem.Time < *startUnix {
			continue
		}
		row, ok := rows[logItem.ChannelId]
		if !ok {
			row = &analyticsProviderAggregateRow{ChannelID: logItem.ChannelId, ChannelName: logItem.ChannelName}
			rows[logItem.ChannelId] = row
		}
		row.InputTokens += int64(logItem.InputTokens)
		row.OutputTokens += int64(logItem.OutputTokens)
		row.TotalCost += logItem.Cost
		if logItem.Error == "" {
			row.RequestSuccess++
		} else {
			row.RequestFailed++
		}
		if row.ChannelName == "" {
			row.ChannelName = logItem.ChannelName
		}
	}
	return rows, nil
}

func loadAnalyticsModelRowsFromRelayLogs(ctx context.Context, r model.AnalyticsRange) (map[string]*analyticsModelAggregateRow, error) {
	startUnix := analyticsRangeStartUnix(r, stats.Now())
	rows := make(map[string]*analyticsModelAggregateRow)
	keepEnabled, err := setting.GetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return nil, err
	}
	if keepEnabled {
		var dbRows []analyticsModelAggregateRow
		modelExpr := "COALESCE(NULLIF(actual_model_name, ''), request_model_name)"
		query := relayLogReadConn().WithContext(ctx).Model(&model.RelayLog{}).
			Select(modelExpr + ` AS model_name, COALESCE(SUM(input_tokens), 0) AS input_tokens, COALESCE(SUM(output_tokens), 0) AS output_tokens, COALESCE(SUM(cost), 0) AS total_cost, COALESCE(SUM(CASE WHEN error = '' THEN 1 ELSE 0 END), 0) AS request_success, COALESCE(SUM(CASE WHEN error <> '' THEN 1 ELSE 0 END), 0) AS request_failed`).
			Group(modelExpr)
		if startUnix != nil {
			query = query.Where("time >= ?", *startUnix)
		}
		if err := query.Scan(&dbRows).Error; err != nil {
			return nil, err
		}
		for _, row := range dbRows {
			modelName := strings.TrimSpace(row.ModelName)
			if modelName == "" {
				continue
			}
			rowCopy := row
			rowCopy.ModelName = modelName
			rows[modelName] = &rowCopy
		}
	}
	cache, lock := relaylog.GetCacheAndLock()
	lock.Lock()
	defer lock.Unlock()
	for _, logItem := range cache {
		if startUnix != nil && logItem.Time < *startUnix {
			continue
		}
		modelName := strings.TrimSpace(logItem.ActualModelName)
		if modelName == "" {
			modelName = strings.TrimSpace(logItem.RequestModelName)
		}
		if modelName == "" {
			continue
		}
		row, ok := rows[modelName]
		if !ok {
			row = &analyticsModelAggregateRow{ModelName: modelName}
			rows[modelName] = row
		}
		row.InputTokens += int64(logItem.InputTokens)
		row.OutputTokens += int64(logItem.OutputTokens)
		row.TotalCost += logItem.Cost
		if logItem.Error == "" {
			row.RequestSuccess++
		} else {
			row.RequestFailed++
		}
	}
	return rows, nil
}

func loadAnalyticsAPIKeyRowsFromRelayLogs(ctx context.Context, r model.AnalyticsRange) (map[string]*analyticsAPIKeyAggregateRow, error) {
	startUnix := analyticsRangeStartUnix(r, stats.Now())
	rows := make(map[string]*analyticsAPIKeyAggregateRow)
	keepEnabled, err := setting.GetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return nil, err
	}
	if keepEnabled {
		var dbRows []analyticsAPIKeyAggregateRow
		query := relayLogReadConn().WithContext(ctx).Model(&model.RelayLog{}).
			Select(`request_api_key_id AS api_key_id, request_api_key_name AS name, COALESCE(SUM(input_tokens), 0) AS input_tokens, COALESCE(SUM(output_tokens), 0) AS output_tokens, COALESCE(SUM(cost), 0) AS total_cost, COALESCE(SUM(CASE WHEN error = '' THEN 1 ELSE 0 END), 0) AS request_success, COALESCE(SUM(CASE WHEN error <> '' THEN 1 ELSE 0 END), 0) AS request_failed`).
			Group("request_api_key_id, request_api_key_name")
		if startUnix != nil {
			query = query.Where("time >= ?", *startUnix)
		}
		if err := query.Scan(&dbRows).Error; err != nil {
			return nil, err
		}
		for _, row := range dbRows {
			rowCopy := row
			rowCopy.Name = strings.TrimSpace(row.Name)
			rows[makeAnalyticsAPIKeyAggregateKey(row.APIKeyID, rowCopy.Name)] = &rowCopy
		}
	}
	cache, lock := relaylog.GetCacheAndLock()
	lock.Lock()
	defer lock.Unlock()
	for _, logItem := range cache {
		if startUnix != nil && logItem.Time < *startUnix {
			continue
		}
		keyName := strings.TrimSpace(logItem.RequestAPIKeyName)
		aggregateKey := makeAnalyticsAPIKeyAggregateKey(logItem.RequestAPIKeyID, keyName)
		row, ok := rows[aggregateKey]
		if !ok {
			row = &analyticsAPIKeyAggregateRow{APIKeyID: logItem.RequestAPIKeyID, Name: keyName}
			rows[aggregateKey] = row
		}
		row.InputTokens += int64(logItem.InputTokens)
		row.OutputTokens += int64(logItem.OutputTokens)
		row.TotalCost += logItem.Cost
		if logItem.Error == "" {
			row.RequestSuccess++
		} else {
			row.RequestFailed++
		}
	}
	return rows, nil
}

func loadAnalyticsChannelModelRowsFromRelayLogs(ctx context.Context, r model.AnalyticsRange, scope channelModelScope) (map[string]*analyticsChannelModelAggregateRow, error) {
	startUnix := analyticsRangeStartUnix(r, stats.Now())
	rows := make(map[string]*analyticsChannelModelAggregateRow)
	inScope := scope.inScope
	keepEnabled, err := setting.GetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return nil, err
	}
	if keepEnabled {
		readConn := relayLogReadConn()
		if readConn != nil {
			if connHasRelayLogAttempts(readConn) {
				// 1) attempts 表 DB 聚合（主路径，无 JSON 装载）
				if err := loadChannelModelRowsFromAttemptsTable(ctx, readConn, startUnix, rows, inScope); err != nil {
					return nil, err
				}
				// 2) 仅有 attempts JSON、无 attempts 表行的历史日志（issue #121 兼容）
				//    用 NOT EXISTS 过滤，避免与 attempts 表双计。
				if err := loadChannelModelRowsFromAttemptsJSONBatched(ctx, readConn, startUnix, rows, inScope, true); err != nil {
					return nil, err
				}
				// 3) 完全无 attempts 信息的日志：顶层列 GROUP BY 补缺
				if err := loadChannelModelRowsFromTopLevelMissingAttempts(ctx, readConn, startUnix, rows, inScope); err != nil {
					return nil, err
				}
			} else {
				// 无 attempts 表：分批 JSON（有 attempts 字段）+ 顶层补缺（无 attempts）
				if err := loadChannelModelRowsFromAttemptsJSONBatched(ctx, readConn, startUnix, rows, inScope, false); err != nil {
					return nil, err
				}
				if err := loadChannelModelRowsFromTopLevelMissingAttempts(ctx, readConn, startUnix, rows, inScope); err != nil {
					return nil, err
				}
			}
		}
	}
	// 内存缓存仍可走完整 merge（含 attempts），体量有界（relayLogMaxSize）。
	cache, lock := relaylog.GetCacheAndLock()
	lock.Lock()
	defer lock.Unlock()
	for _, logItem := range cache {
		if startUnix != nil && logItem.Time < *startUnix {
			continue
		}
		mergeRelayLogIntoChannelModelRows(rows, &logItem, inScope)
	}
	return rows, nil
}

// loadChannelModelRowsFromAttemptsTable 从 relay_log_attempts 做 DB 端聚合。
// token/cost 仅在 status=success 且父日志 error 为空时计入（与 mergeRelayLogIntoChannelModelRows 一致）。
func loadChannelModelRowsFromAttemptsTable(
	ctx context.Context,
	conn *gorm.DB,
	startUnix *int64,
	rows map[string]*analyticsChannelModelAggregateRow,
	inScope func(int, string) bool,
) error {
	type attemptAggRow struct {
		ChannelID      int     `gorm:"column:channel_id"`
		ChannelName    string  `gorm:"column:channel_name"`
		ModelName      string  `gorm:"column:model_name"`
		InputTokens    int64   `gorm:"column:input_tokens"`
		OutputTokens   int64   `gorm:"column:output_tokens"`
		TotalCost      float64 `gorm:"column:total_cost"`
		RequestSuccess int64   `gorm:"column:request_success"`
		RequestFailed  int64   `gorm:"column:request_failed"`
	}
	var dbRows []attemptAggRow
	query := conn.WithContext(ctx).
		Table("relay_log_attempts AS a").
		Select(`
			a.channel_id,
			MAX(a.channel_name) AS channel_name,
			COALESCE(NULLIF(a.model_name, ''), NULLIF(l.actual_model_name, ''), l.request_model_name) AS model_name,
			COALESCE(SUM(CASE WHEN a.status = ? AND l.error = '' THEN l.input_tokens ELSE 0 END), 0) AS input_tokens,
			COALESCE(SUM(CASE WHEN a.status = ? AND l.error = '' THEN l.output_tokens ELSE 0 END), 0) AS output_tokens,
			COALESCE(SUM(CASE WHEN a.status = ? AND l.error = '' THEN l.cost ELSE 0 END), 0) AS total_cost,
			COALESCE(SUM(CASE WHEN a.status = ? THEN 1 ELSE 0 END), 0) AS request_success,
			COALESCE(SUM(CASE WHEN a.status = ? THEN 1 ELSE 0 END), 0) AS request_failed
		`, string(model.AttemptSuccess), string(model.AttemptSuccess), string(model.AttemptSuccess), string(model.AttemptSuccess), string(model.AttemptFailed)).
		Joins("JOIN relay_logs AS l ON l.id = a.relay_log_id").
		Group("a.channel_id, COALESCE(NULLIF(a.model_name, ''), NULLIF(l.actual_model_name, ''), l.request_model_name)")
	if startUnix != nil {
		query = query.Where("a.time >= ?", *startUnix)
	}
	if err := query.Scan(&dbRows).Error; err != nil {
		return err
	}
	for _, row := range dbRows {
		modelName := strings.TrimSpace(row.ModelName)
		if modelName == "" || row.ChannelID == 0 || !inScope(row.ChannelID, modelName) {
			continue
		}
		key := strconv.Itoa(row.ChannelID) + "\x00" + modelName
		rows[key] = &analyticsChannelModelAggregateRow{
			ChannelID:      row.ChannelID,
			ChannelName:    strings.TrimSpace(row.ChannelName),
			ModelName:      modelName,
			InputTokens:    row.InputTokens,
			OutputTokens:   row.OutputTokens,
			TotalCost:      row.TotalCost,
			RequestSuccess: row.RequestSuccess,
			RequestFailed:  row.RequestFailed,
		}
	}
	return nil
}

// loadChannelModelRowsFromTopLevelMissingAttempts 只聚合「无 attempts 信息」的日志，
// 避免与 attempts 表 / attempts JSON 路径双计。
func loadChannelModelRowsFromTopLevelMissingAttempts(
	ctx context.Context,
	conn *gorm.DB,
	startUnix *int64,
	rows map[string]*analyticsChannelModelAggregateRow,
	inScope func(int, string) bool,
) error {
	modelExpr := "COALESCE(NULLIF(actual_model_name, ''), request_model_name)"
	var dbRows []analyticsChannelModelAggregateRow
	query := conn.WithContext(ctx).Model(&model.RelayLog{}).
		Select(`
			channel_id,
			MAX(channel_name) AS channel_name,
			` + modelExpr + ` AS model_name,
			COALESCE(SUM(input_tokens), 0) AS input_tokens,
			COALESCE(SUM(output_tokens), 0) AS output_tokens,
			COALESCE(SUM(cost), 0) AS total_cost,
			COALESCE(SUM(CASE WHEN error = '' THEN 1 ELSE 0 END), 0) AS request_success,
			COALESCE(SUM(CASE WHEN error <> '' THEN 1 ELSE 0 END), 0) AS request_failed
		`).
		// 无 attempts JSON，且（若表存在）无 attempts 表行。
		Where("(attempts IS NULL OR attempts = '' OR attempts = 'null' OR attempts = '[]')").
		Group("channel_id, " + modelExpr)
	if startUnix != nil {
		query = query.Where("time >= ?", *startUnix)
	}
	// 有 attempts 表时排除已有关联行的父日志（双保险）。
	if connHasRelayLogAttempts(conn) {
		query = query.Where("NOT EXISTS (SELECT 1 FROM relay_log_attempts a WHERE a.relay_log_id = relay_logs.id)")
	}
	if err := query.Scan(&dbRows).Error; err != nil {
		return err
	}
	for _, row := range dbRows {
		modelName := strings.TrimSpace(row.ModelName)
		if modelName == "" || row.ChannelID == 0 || !inScope(row.ChannelID, modelName) {
			continue
		}
		key := strconv.Itoa(row.ChannelID) + "\x00" + modelName
		if existing, ok := rows[key]; ok {
			existing.InputTokens += row.InputTokens
			existing.OutputTokens += row.OutputTokens
			existing.TotalCost += row.TotalCost
			existing.RequestSuccess += row.RequestSuccess
			existing.RequestFailed += row.RequestFailed
			if existing.ChannelName == "" {
				existing.ChannelName = strings.TrimSpace(row.ChannelName)
			}
			continue
		}
		rowCopy := row
		rowCopy.ModelName = modelName
		rowCopy.ChannelName = strings.TrimSpace(row.ChannelName)
		rows[key] = &rowCopy
	}
	return nil
}

// channelModelAttemptsJSONBatchSize 控制 attempts JSON 回退路径的峰值内存。
const channelModelAttemptsJSONBatchSize = 500

// loadChannelModelRowsFromAttemptsJSONBatched 分批读 attempts JSON（不读 content 大字段）。
// onlyMissingAttemptsTable=true 时跳过已有 attempts 表行的父日志，避免双计。
func loadChannelModelRowsFromAttemptsJSONBatched(
	ctx context.Context,
	conn *gorm.DB,
	startUnix *int64,
	rows map[string]*analyticsChannelModelAggregateRow,
	inScope func(int, string) bool,
	onlyMissingAttemptsTable bool,
) error {
	type rowWithID struct {
		ID               int64                  `gorm:"column:id"`
		ChannelId        int                    `gorm:"column:channel_id"`
		ChannelName      string                 `gorm:"column:channel_name"`
		RequestModelName string                 `gorm:"column:request_model_name"`
		ActualModelName  string                 `gorm:"column:actual_model_name"`
		InputTokens      int                    `gorm:"column:input_tokens"`
		OutputTokens     int                    `gorm:"column:output_tokens"`
		Cost             float64                `gorm:"column:cost"`
		Error            string                 `gorm:"column:error"`
		Attempts         []model.ChannelAttempt `gorm:"column:attempts;serializer:json"`
	}
	var lastID int64
	for {
		var idBatch []rowWithID
		query := conn.WithContext(ctx).Table("relay_logs").
			Select("id", "channel_id", "channel_name", "request_model_name", "actual_model_name", "input_tokens", "output_tokens", "cost", "error", "attempts").
			Where("attempts IS NOT NULL AND attempts <> '' AND attempts <> 'null' AND attempts <> '[]'").
			Order("id ASC").
			Limit(channelModelAttemptsJSONBatchSize)
		if startUnix != nil {
			query = query.Where("time >= ?", *startUnix)
		}
		if lastID > 0 {
			query = query.Where("id > ?", lastID)
		}
		if onlyMissingAttemptsTable {
			query = query.Where("NOT EXISTS (SELECT 1 FROM relay_log_attempts a WHERE a.relay_log_id = relay_logs.id)")
		}
		if err := query.Find(&idBatch).Error; err != nil {
			return err
		}
		if len(idBatch) == 0 {
			break
		}
		for i := range idBatch {
			lite := idBatch[i]
			lastID = lite.ID
			if len(lite.Attempts) == 0 {
				continue
			}
			mergeRelayLogIntoChannelModelRows(rows, &model.RelayLog{
				ChannelId:        lite.ChannelId,
				ChannelName:      lite.ChannelName,
				RequestModelName: lite.RequestModelName,
				ActualModelName:  lite.ActualModelName,
				InputTokens:      lite.InputTokens,
				OutputTokens:     lite.OutputTokens,
				Cost:             lite.Cost,
				Error:            lite.Error,
				Attempts:         lite.Attempts,
			}, inScope)
		}
		if len(idBatch) < channelModelAttemptsJSONBatchSize {
			break
		}
	}
	return nil
}

func makeAnalyticsAPIKeyAggregateKey(apiKeyID int, name string) string {
	if apiKeyID > 0 {
		return "id:" + strconv.Itoa(apiKeyID)
	}
	return "name:" + strings.TrimSpace(name)
}

func loadAnalyticsFailureRows(ctx context.Context, since time.Time) (map[string]*analyticsFailureAggregateRow, error) {
	startUnix := since.Unix()
	rows := make(map[string]*analyticsFailureAggregateRow)

	keepEnabled, err := setting.GetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return nil, err
	}

	if keepEnabled {
		// 优先从 relay_log_attempts 聚合失败尝试，使"渠道A 失败→重试到B 成功"
		// 的请求中渠道A 的失败也被计入（issue #67）。join relay_logs 取
		// request_model_name（分组名）以保留与 GroupItem.ModelName 的匹配维度。
		// 依次尝试 LogDB → 主库的 attempts 表，最后才回退到顶层列。
		var attemptsConn *gorm.DB
		conn := db.GetLogDB()
		if conn != nil && connHasRelayLogAttempts(conn) {
			attemptsConn = conn
		} else if mainConn := db.GetDB(); mainConn != nil && connHasRelayLogAttempts(mainConn) {
			attemptsConn = mainConn
		}

		if attemptsConn != nil {
			var dbRows []analyticsFailureAggregateRow
			query := attemptsConn.WithContext(ctx).
				Table("relay_log_attempts AS a").
				Select(`
					a.channel_id,
					l.request_model_name,
					a.model_name AS actual_model_name,
					COUNT(*) AS failure_count,
					MAX(a.time) AS last_failure_at
				`).
				Joins("JOIN relay_logs AS l ON l.id = a.relay_log_id").
				Where("a.status = ?", string(model.AttemptFailed)).
				Where("a.time >= ?", startUnix).
				Group("a.channel_id, l.request_model_name, a.model_name")
			if err := query.Scan(&dbRows).Error; err != nil {
				return nil, err
			}
			for _, row := range dbRows {
				key := makeAnalyticsFailureKey(row.ChannelID, row.ActualModelName, row.RequestModelName)
				rowCopy := row
				rows[key] = &rowCopy
			}
		} else {
			// 最终回退：LogDB 和主库均无 attempts 表时用顶层 relay_logs 列。
			conn := db.GetDB()
			if conn != nil {
				var dbRows []analyticsFailureAggregateRow
				query := conn.WithContext(ctx).
					Model(&model.RelayLog{}).
					Select(`
						channel_id,
						request_model_name,
						actual_model_name,
						COUNT(*) AS failure_count,
						MAX(time) AS last_failure_at
					`).
					Where("error <> ''").
					Where("time >= ?", startUnix).
					Group("channel_id, request_model_name, actual_model_name")
				if err := query.Scan(&dbRows).Error; err != nil {
					return nil, err
				}
				for _, row := range dbRows {
					key := makeAnalyticsFailureKey(row.ChannelID, row.ActualModelName, row.RequestModelName)
					rowCopy := row
					rows[key] = &rowCopy
				}
			}
		}
	}

	// 内存缓存中尚未落库的失败尝试同样按尝试维度聚合。
	cache, lock := relaylog.GetCacheAndLock()
	lock.Lock()
	for _, logItem := range cache {
		if logItem.Time < startUnix {
			continue
		}
		// 整体失败：用顶层渠道记一次（与历史行为一致）。
		if logItem.Error != "" {
			key := makeAnalyticsFailureKey(logItem.ChannelId, logItem.ActualModelName, logItem.RequestModelName)
			row, ok := rows[key]
			if !ok {
				row = &analyticsFailureAggregateRow{
					ChannelID:        logItem.ChannelId,
					RequestModelName: logItem.RequestModelName,
					ActualModelName:  logItem.ActualModelName,
				}
				rows[key] = row
			}
			row.FailureCount++
			if logItem.Time > row.LastFailureAt {
				row.LastFailureAt = logItem.Time
			}
			continue
		}
		// 整体成功但含失败尝试：把每个失败尝试计入对应渠道（issue #67 关键修复）。
		for _, a := range logItem.Attempts {
			if a.Status != model.AttemptFailed || a.ChannelID == 0 {
				continue
			}
			key := makeAnalyticsFailureKey(a.ChannelID, a.ModelName, logItem.RequestModelName)
			row, ok := rows[key]
			if !ok {
				row = &analyticsFailureAggregateRow{
					ChannelID:        a.ChannelID,
					RequestModelName: logItem.RequestModelName,
					ActualModelName:  a.ModelName,
				}
				rows[key] = row
			}
			row.FailureCount++
			if logItem.Time > row.LastFailureAt {
				row.LastFailureAt = logItem.Time
			}
		}
	}
	lock.Unlock()

	return rows, nil
}

// connHasRelayLogAttempts 报告连接上是否已存在 relay_log_attempts 表（迁移后才有）。
// 用于在 DB 与 LogDB 分离、或旧库尚未迁移时优雅回退到顶层列聚合。
func connHasRelayLogAttempts(conn *gorm.DB) bool {
	if conn == nil || conn.Migrator() == nil {
		return false
	}
	return conn.Migrator().HasTable(&model.RelayLogAttempt{})
}

// relayLogReadConn 返回承载 relay_logs 数据的连接。
// 独立日志库模式下 relay_logs 写入 LogDB（见 relaylog.relayLogFlushToDB），
// 主库的 relay_logs 表为空。若仍用 db.GetDB() 读取，则除内存缓存外的历史
// 日志全部缺失——表现为「按模型/按渠道/按 API Key/延迟分布」在 1d 偶有数据、
// 7d 起无数据（issue #101）。优先取 LogDB，缺失时回退主库，与共用主库模式一致。
func relayLogReadConn() *gorm.DB {
	if conn := db.GetLogDB(); conn != nil {
		return conn
	}
	return db.GetDB()
}

func analyticsRangeStartUnix(r model.AnalyticsRange, now time.Time) *int64 {
	startDate := analyticsStartTime(r, now)
	if startDate == nil {
		return nil
	}
	unix := startDate.Unix()
	return &unix
}

func analyticsStartDate(r model.AnalyticsRange, now time.Time) string {
	start := analyticsStartTime(r, now)
	if start == nil {
		return ""
	}
	return start.Format("20060102")
}

func analyticsStartTime(r model.AnalyticsRange, now time.Time) *time.Time {
	location := now.Location()
	// dayStart uses now.Location() which reflects the container TZ, not stats offset.
	// Future: if stats_timezone is promoted to IANA, consider whether analytics
	// should also switch or remain on server local time.
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)

	switch r {
	case model.AnalyticsRange1D:
		return &dayStart
	case model.AnalyticsRange7D:
		start := dayStart.AddDate(0, 0, -6)
		return &start
	case model.AnalyticsRange30D:
		start := dayStart.AddDate(0, 0, -29)
		return &start
	case model.AnalyticsRange90D:
		start := dayStart.AddDate(0, 0, -89)
		return &start
	case model.AnalyticsRangeYTD:
		start := time.Date(now.Year(), time.January, 1, 0, 0, 0, 0, location)
		return &start
	case model.AnalyticsRangeAll:
		return nil
	default:
		start := dayStart.AddDate(0, 0, -6)
		return &start
	}
}

// AnalyticsLatencyDistributionGet returns latency and FTUT distribution for the given range.
func AnalyticsLatencyDistributionGet(ctx context.Context, r model.AnalyticsRange) (*model.LatencyDistribution, error) {
	return loadLatencyDistribution(ctx, r)
}

func loadLatencyDistribution(ctx context.Context, r model.AnalyticsRange) (*model.LatencyDistribution, error) {
	startUnix := analyticsRangeStartUnix(r, stats.Now())
	result := &model.LatencyDistribution{}

	keepEnabled, err := setting.GetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return nil, err
	}

	// DB 端聚合：单次查询返回 count/sum + 5 个延迟桶 + 5 个首字桶，避免把百万行
	// use_time/ftut 全量拉进内存排序（90d/all 下的内存爆炸与磁盘读飙升）。
	// 百分位由桶边界线性插值近似，对分析概览场景精度足够。
	var hLt100, h100to500, h500to1k, h1kto5k, hGt5k int64
	var fhLt100, fh100to500, fh500to1k, fh1kto5k, fhGt5k int64
	var totalUseTime, totalFtut, totalCount, ftutCount int64

	if keepEnabled {
		type aggRow struct {
			TotalCount int64 `gorm:"column:total_count"`
			TotalUse   int64 `gorm:"column:total_use"`
			TotalFtut  int64 `gorm:"column:total_ftut"`
			FtutCount  int64 `gorm:"column:ftut_count"`
			HLt100     int64 `gorm:"column:h_lt100"`
			H100to500  int64 `gorm:"column:h_100_500"`
			H500to1k   int64 `gorm:"column:h_500_1k"`
			H1kto5k    int64 `gorm:"column:h_1k_5k"`
			HGt5k      int64 `gorm:"column:h_gt5k"`
			FHLt100    int64 `gorm:"column:fh_lt100"`
			FH100to500 int64 `gorm:"column:fh_100_500"`
			FH500to1k  int64 `gorm:"column:fh_500_1k"`
			FH1kto5k   int64 `gorm:"column:fh_1k_5k"`
			FHGt5k     int64 `gorm:"column:fh_gt5k"`
		}
		var row aggRow
		query := relayLogReadConn().WithContext(ctx).Model(&model.RelayLog{}).
			Select(`
				COUNT(CASE WHEN use_time > 0 THEN 1 END) AS total_count,
				COALESCE(SUM(CASE WHEN use_time > 0 THEN use_time ELSE 0 END), 0) AS total_use,
				COALESCE(SUM(CASE WHEN ftut > 0 THEN ftut ELSE 0 END), 0) AS total_ftut,
				COUNT(CASE WHEN ftut > 0 THEN 1 END) AS ftut_count,
				COUNT(CASE WHEN use_time > 0 AND use_time < 100 THEN 1 END) AS h_lt100,
				COUNT(CASE WHEN use_time >= 100 AND use_time < 500 THEN 1 END) AS h_100_500,
				COUNT(CASE WHEN use_time >= 500 AND use_time < 1000 THEN 1 END) AS h_500_1k,
				COUNT(CASE WHEN use_time >= 1000 AND use_time < 5000 THEN 1 END) AS h_1k_5k,
				COUNT(CASE WHEN use_time >= 5000 THEN 1 END) AS h_gt5k,
				COUNT(CASE WHEN ftut > 0 AND ftut < 100 THEN 1 END) AS fh_lt100,
				COUNT(CASE WHEN ftut >= 100 AND ftut < 500 THEN 1 END) AS fh_100_500,
				COUNT(CASE WHEN ftut >= 500 AND ftut < 1000 THEN 1 END) AS fh_500_1k,
				COUNT(CASE WHEN ftut >= 1000 AND ftut < 5000 THEN 1 END) AS fh_1k_5k,
				COUNT(CASE WHEN ftut >= 5000 THEN 1 END) AS fh_gt5k
			`)
		if startUnix != nil {
			query = query.Where("time >= ?", *startUnix)
		}
		if err := query.Scan(&row).Error; err != nil {
			return nil, err
		}
		totalCount = row.TotalCount
		totalUseTime = row.TotalUse
		totalFtut = row.TotalFtut
		ftutCount = row.FtutCount
		hLt100 = row.HLt100
		h100to500 = row.H100to500
		h500to1k = row.H500to1k
		h1kto5k = row.H1kto5k
		hGt5k = row.HGt5k
		fhLt100 = row.FHLt100
		fh100to500 = row.FH100to500
		fh500to1k = row.FH500to1k
		fh1kto5k = row.FH1kto5k
		fhGt5k = row.FHGt5k
	}

	// 合并内存缓存（尚未落库的日志，≤200 条，逐条处理无内存压力）。
	cache, lock := relaylog.GetCacheAndLock()
	lock.Lock()
	for _, logItem := range cache {
		if startUnix != nil && logItem.Time < *startUnix {
			continue
		}
		if logItem.UseTime > 0 {
			totalUseTime += int64(logItem.UseTime)
			totalCount++
			switch {
			case logItem.UseTime < 100:
				hLt100++
			case logItem.UseTime < 500:
				h100to500++
			case logItem.UseTime < 1000:
				h500to1k++
			case logItem.UseTime < 5000:
				h1kto5k++
			default:
				hGt5k++
			}
		}
		if logItem.Ftut > 0 {
			totalFtut += int64(logItem.Ftut)
			ftutCount++
			switch {
			case logItem.Ftut < 100:
				fhLt100++
			case logItem.Ftut < 500:
				fh100to500++
			case logItem.Ftut < 1000:
				fh500to1k++
			case logItem.Ftut < 5000:
				fh1kto5k++
			default:
				fhGt5k++
			}
		}
	}
	lock.Unlock()

	result.TotalRequests = totalCount
	if totalCount > 0 {
		result.AvgMs = totalUseTime / totalCount
	}
	if ftutCount > 0 {
		result.FtutAvgMs = totalFtut / ftutCount
	}

	// 百分位由桶边界线性插值近似。
	latBuckets := []histBucket{
		{0, 100, hLt100}, {100, 500, h100to500}, {500, 1000, h500to1k},
		{1000, 5000, h1kto5k}, {5000, -1, hGt5k},
	}
	result.P50Ms = percentileFromBuckets(latBuckets, totalCount, 0.50)
	result.P95Ms = percentileFromBuckets(latBuckets, totalCount, 0.95)
	result.P99Ms = percentileFromBuckets(latBuckets, totalCount, 0.99)

	ftutBuckets := []histBucket{
		{0, 100, fhLt100}, {100, 500, fh100to500}, {500, 1000, fh500to1k},
		{1000, 5000, fh1kto5k}, {5000, -1, fhGt5k},
	}
	result.FtutP50Ms = percentileFromBuckets(ftutBuckets, ftutCount, 0.50)
	result.FtutP95Ms = percentileFromBuckets(ftutBuckets, ftutCount, 0.95)
	result.FtutP99Ms = percentileFromBuckets(ftutBuckets, ftutCount, 0.99)

	result.Buckets = []model.HistogramBucket{
		{Label: "<100ms", Count: hLt100},
		{Label: "100-500ms", Count: h100to500},
		{Label: "500ms-1s", Count: h500to1k},
		{Label: "1-5s", Count: h1kto5k},
		{Label: ">5s", Count: hGt5k},
	}

	return result, nil
}

// histBucket 描述一个直方图桶：[lo, hi)，hi<0 表示无上界（末桶）。
type histBucket struct {
	lo    int64
	hi    int64 // -1 = 无上界
	count int64
}

// percentileFromBuckets 由直方图桶线性插值近似百分位。
// 在桶内按均匀分布假设插值；首桶（lo=0）从 0 起，末桶（hi<0）取 lo 作为保守下界。
// total 为全部桶 count 之和。total=0 返回 0。
func percentileFromBuckets(buckets []histBucket, total int64, p float64) int64 {
	if total <= 0 {
		return 0
	}
	target := p * float64(total)
	var cum int64
	for _, b := range buckets {
		if b.count == 0 {
			continue
		}
		next := cum + b.count
		if float64(next) >= target {
			// 目标落在本桶内，线性插值。
			frac := 0.0
			if b.count > 0 {
				frac = (target - float64(cum)) / float64(b.count)
			}
			if b.hi < 0 {
				// 末桶无上界：保守返回桶下界，避免外推出不合理大值。
				return b.lo
			}
			return b.lo + int64(frac*float64(b.hi-b.lo))
		}
		cum = next
	}
	// 兜底：返回最大桶上界。
	for i := len(buckets) - 1; i >= 0; i-- {
		if buckets[i].count > 0 {
			if buckets[i].hi < 0 {
				return buckets[i].lo
			}
			return buckets[i].hi
		}
	}
	return 0
}

func splitAnalyticsChannelModels(channel model.Channel) []string {
	parts := strings.Split(channel.Model, ",")
	if strings.TrimSpace(channel.CustomModel) != "" {
		parts = append(parts, strings.Split(channel.CustomModel, ",")...)
	}

	seen := make(map[string]struct{}, len(parts))
	models := make([]string, 0, len(parts))
	for _, part := range parts {
		modelName := strings.TrimSpace(part)
		if modelName == "" {
			continue
		}
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		models = append(models, modelName)
	}
	return models
}

func makeAnalyticsFailureKey(channelID int, actualModelName, requestModelName string) string {
	actualModelName = strings.TrimSpace(actualModelName)
	if actualModelName == "" {
		actualModelName = strings.TrimSpace(requestModelName)
	}
	return strings.Join([]string{
		strconv.Itoa(channelID),
		actualModelName,
		strings.TrimSpace(requestModelName),
	}, "\x00")
}
