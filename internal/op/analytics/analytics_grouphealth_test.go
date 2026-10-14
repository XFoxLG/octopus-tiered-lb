package analytics

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/op/group"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/relay/balancer"
)

// TestBuildGroupHealth_SurfacesFailingChannelFromAttempts 验证 buildGroupHealth 把
// "渠道A 失败→重试到B 成功"中的渠道A 失败计入组的 failureCount，并在 FailingChannels
// 下钻列表中暴露（issue #67 核心修复）。
func TestBuildGroupHealth_SurfacesFailingChannelFromAttempts(t *testing.T) {
	groups := []model.Group{
		{
			ID:   1,
			Name: "gpt-4o",
			Mode: model.GroupModeFailover,
			Items: []model.GroupItem{
				{ChannelID: 11, ModelName: "gpt-4o"},
				{ChannelID: 22, ModelName: "gpt-4o"},
			},
		},
	}
	channelByID := map[int]model.Channel{
		11: {ID: 11, Name: "channelA", Enabled: true},
		22: {ID: 22, Name: "channelB", Enabled: true},
	}

	// 渠道A 在该模型上有 3 次失败（来自 attempts 聚合）。
	failures := map[string]*analyticsFailureAggregateRow{
		makeAnalyticsFailureKey(11, "gpt-4o", "gpt-4o"): {
			ChannelID:        11,
			RequestModelName: "gpt-4o",
			ActualModelName:  "gpt-4o",
			FailureCount:     3,
			LastFailureAt:    1700_000_100,
		},
	}

	items := buildGroupHealth(groups, channelByID, failures, nil, 0)
	if len(items) != 1 {
		t.Fatalf("expected 1 group health item, got %d", len(items))
	}
	item := items[0]
	if item.FailureCount != 3 {
		t.Fatalf("FailureCount = %d, want 3", item.FailureCount)
	}
	if item.Status != "degraded" {
		t.Fatalf("Status = %q, want degraded (failureCount>=3)", item.Status)
	}
	if len(item.FailingChannels) != 1 {
		t.Fatalf("FailingChannels len = %d, want 1", len(item.FailingChannels))
	}
	fc := item.FailingChannels[0]
	if fc.ChannelID != 11 || fc.ChannelName != "channelA" {
		t.Fatalf("failing channel = %+v, want channelA(11)", fc)
	}
	if fc.FailureCount != 3 {
		t.Fatalf("failing channel FailureCount = %d, want 3", fc.FailureCount)
	}
}

// TestLoadAnalyticsFailureRows_AggregatesPerAttemptFromCache 验证内存缓存中整体成功
// 但含失败尝试的请求，会把失败计入对应渠道（而非顶层成功渠道）。
func TestLoadAnalyticsFailureRows_AggregatesPerAttemptFromCache(t *testing.T) {
	restoreLogs := relaylog.SetCacheForTest([]model.RelayLog{
		{
			Time: time.Now().Unix(),
			// 顶层渠道B、整体成功
			ChannelId:        22,
			ActualModelName:  "gpt-4o",
			RequestModelName: "gpt-4o",
			Error:            "",
			Attempts: []model.ChannelAttempt{
				{ChannelID: 11, ModelName: "gpt-4o", Status: model.AttemptFailed},
				{ChannelID: 22, ModelName: "gpt-4o", Status: model.AttemptSuccess},
			},
		},
	})
	defer restoreLogs()

	settingCache := setting.GetCache()
	oldSettings := settingCache.GetAll()
	settingCache.Set(model.SettingKeyRelayLogKeepEnabled, "false")
	defer func() {
		settingCache.Clear()
		for k, v := range oldSettings {
			settingCache.Set(k, v)
		}
	}()

	failures, err := loadAnalyticsFailureRows(context.Background(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("loadAnalyticsFailureRows error: %v", err)
	}

	// 渠道A(11) 应有 1 次失败；顶层渠道B(22) 整体成功不应被计入。
	rowA, ok := failures[makeAnalyticsFailureKey(11, "gpt-4o", "gpt-4o")]
	if !ok || rowA == nil {
		t.Fatalf("expected failure row for channelA(11), got nil")
	}
	if rowA.FailureCount != 1 {
		t.Fatalf("channelA FailureCount = %d, want 1", rowA.FailureCount)
	}
	if _, ok := failures[makeAnalyticsFailureKey(22, "gpt-4o", "gpt-4o")]; ok {
		t.Fatalf("channelB(22) should NOT appear in failures (overall success)")
	}
}

// TestLoadAnalyticsChannelModelRows_CountsPerAttemptFailures 验证渠道×模型聚合把
// 重试场景中失败渠道的失败计入其成功率。
func TestLoadAnalyticsChannelModelRows_CountsPerAttemptFailures(t *testing.T) {
	restoreLogs := relaylog.SetCacheForTest([]model.RelayLog{
		{
			Time: time.Now().Unix(),
			// 顶层渠道B、整体成功；含 token/cost
			ChannelId:        22,
			ActualModelName:  "gpt-4o",
			RequestModelName: "gpt-4o",
			Error:            "",
			InputTokens:      100,
			OutputTokens:     50,
			Cost:             0.2,
			Attempts: []model.ChannelAttempt{
				{ChannelID: 11, ModelName: "gpt-4o", Status: model.AttemptFailed},
				{ChannelID: 22, ModelName: "gpt-4o", Status: model.AttemptSuccess},
			},
		},
	})
	defer restoreLogs()

	settingCache := setting.GetCache()
	oldSettings := settingCache.GetAll()
	settingCache.Set(model.SettingKeyRelayLogKeepEnabled, "false")
	defer func() {
		settingCache.Clear()
		for k, v := range oldSettings {
			settingCache.Set(k, v)
		}
	}()

	rows, err := loadAnalyticsChannelModelRows(context.Background(), model.AnalyticsRange7D, channelModelScope{})
	if err != nil {
		t.Fatalf("loadAnalyticsChannelModelRows error: %v", err)
	}

	rowA, ok := rows["11\x00gpt-4o"]
	if !ok || rowA == nil {
		t.Fatalf("expected row for channelA(11)+gpt-4o")
	}
	// 渠道A：1 次失败、0 次成功；成功率 0%
	if rowA.RequestFailed != 1 || rowA.RequestSuccess != 0 {
		t.Fatalf("channelA got success=%d failed=%d, want success=0 failed=1", rowA.RequestSuccess, rowA.RequestFailed)
	}

	rowB, ok := rows["22\x00gpt-4o"]
	if !ok || rowB == nil {
		t.Fatalf("expected row for channelB(22)+gpt-4o")
	}
	// 渠道B：1 次成功；token/cost 计入（整体成功）
	if rowB.RequestSuccess != 1 || rowB.RequestFailed != 0 {
		t.Fatalf("channelB got success=%d failed=%d, want success=1 failed=0", rowB.RequestSuccess, rowB.RequestFailed)
	}
	if rowB.InputTokens != 100 || rowB.OutputTokens != 50 {
		t.Fatalf("channelB tokens in=%d out=%d, want 100/50", rowB.InputTokens, rowB.OutputTokens)
	}
}

// TestAnalyticsChannelModelBreakdownGet_DBScope 验证 DB 路径下 attempts 表聚合 +
// 分组 scope 过滤。
func TestAnalyticsChannelModelBreakdownGet_DBScope(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "analytics-cm.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatalf("InitLogDB(shared) failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	if err := setting.SetString(model.SettingKeyRelayLogKeepEnabled, "true"); err != nil {
		t.Fatalf("enable relay log keep failed: %v", err)
	}

	// 顶层渠道B、整体成功；attempts 表含渠道A 失败 + 渠道B 成功。
	relayLog := model.RelayLog{
		ID: 1, Time: time.Now().Unix(),
		RequestModelName: "gpt-4o", ActualModelName: "gpt-4o",
		ChannelId: 22, ChannelName: "channelB", Error: "",
		InputTokens: 100, OutputTokens: 50, Cost: 0.2, TotalAttempts: 2,
		Attempts: []model.ChannelAttempt{
			{ChannelID: 11, ChannelName: "channelA", ModelName: "gpt-4o", Status: model.AttemptFailed},
			{ChannelID: 22, ChannelName: "channelB", ModelName: "gpt-4o", Status: model.AttemptSuccess},
		},
	}
	if err := db.GetLogDB().Create(&relayLog).Error; err != nil {
		t.Fatalf("seed relay log failed: %v", err)
	}
	if err := relaylog.RelayLogAttemptsAdd(context.Background(), 1, relayLog.Attempts, relayLog.Time); err != nil {
		t.Fatalf("RelayLogAttemptsAdd error: %v", err)
	}

	restore := relaylog.SetCacheForTest(nil)
	t.Cleanup(restore)

	items, err := AnalyticsChannelModelBreakdownGet(context.Background(), model.AnalyticsRange7D, nil)
	if err != nil {
		t.Fatalf("AnalyticsChannelModelBreakdownGet error: %v", err)
	}

	// 找到渠道A 的行：应记 1 次失败、0 次成功。
	var foundA, foundB bool
	for _, it := range items {
		if it.ChannelID == 11 && it.ModelName == "gpt-4o" {
			foundA = true
			if it.RequestCount != 1 || it.SuccessRate != 0 {
				t.Fatalf("channelA request_count=%d success_rate=%f, want 1/0", it.RequestCount, it.SuccessRate)
			}
		}
		if it.ChannelID == 22 && it.ModelName == "gpt-4o" {
			foundB = true
			if it.RequestCount != 1 || it.SuccessRate < 99.9 || it.SuccessRate > 100.1 {
				t.Fatalf("channelB request_count=%d success_rate=%f, want 1/100", it.RequestCount, it.SuccessRate)
			}
		}
	}
	if !foundA {
		t.Fatalf("channelA row missing from channel-model breakdown")
	}
	if !foundB {
		t.Fatalf("channelB row missing from channel-model breakdown")
	}
}

// TestBuildGroupHealth_AutoItemsFilteredByChannelModel 验证 Auto 组的 AutoItems 按
// 本组 (channel_id, model_name) 精确过滤，跨组渠道的他组模型不会泄漏（issue #87 Bug 回归）。
//
// 场景：渠道1 同时属于组A（chat，model-X）和组B（embeddings，model-Y）。
// 修复前：组A 的 AutoItems 会包含 (渠道1, model-Y)——因为只按 channel_id 过滤。
// 修复后：组A 只含 (渠道1, model-X) 与 (渠道2, model-X)；组B 只含 (渠道1, model-Y)。
func TestBuildGroupHealth_AutoItemsFilteredByChannelModel(t *testing.T) {
	groups := []model.Group{
		{
			ID:           1,
			Name:         "chat-group",
			Mode:         model.GroupModeAuto,
			EndpointType: "chat",
			Items: []model.GroupItem{
				{ChannelID: 1, ModelName: "model-X"},
				{ChannelID: 2, ModelName: "model-X"},
			},
		},
		{
			ID:           2,
			Name:         "embeddings-group",
			Mode:         model.GroupModeAuto,
			EndpointType: "embeddings",
			Items: []model.GroupItem{
				{ChannelID: 1, ModelName: "model-Y"},
			},
		},
	}
	channelByID := map[int]model.Channel{
		1: {ID: 1, Name: "channelShared", Enabled: true},
		2: {ID: 2, Name: "channelB", Enabled: true},
	}

	// 全量 Auto 快照：渠道1 在两个模型上都有数据，渠道2 只有 model-X。
	autoSnapshot := []balancer.AutoStatsSnapshotItem{
		{ChannelID: 1, ModelName: "model-x", SuccessRate: 0.9, SampleCount: 20, AvgLatencyMs: 100, LastActiveAt: time.Now()},
		{ChannelID: 1, ModelName: "model-y", SuccessRate: 0.5, SampleCount: 10, AvgLatencyMs: 200, LastActiveAt: time.Now()},
		{ChannelID: 2, ModelName: "model-x", SuccessRate: 0.8, SampleCount: 15, AvgLatencyMs: 120, LastActiveAt: time.Now()},
	}

	items := buildGroupHealth(groups, channelByID, nil, autoSnapshot, 10)

	if len(items) != 2 {
		t.Fatalf("expected 2 group health items, got %d", len(items))
	}

	byName := make(map[string]model.AnalyticsGroupHealthItem, len(items))
	for _, it := range items {
		byName[it.GroupName] = it
	}

	// 组A（chat-group）：应只含 model-X 的两条（渠道1+渠道2），不含 model-Y。
	groupA := byName["chat-group"]
	if len(groupA.AutoItems) != 2 {
		t.Fatalf("chat-group AutoItems len = %d, want 2 (model-X only)", len(groupA.AutoItems))
	}
	for _, ai := range groupA.AutoItems {
		if ai.ModelName != "model-x" {
			t.Fatalf("chat-group leaked model %q (channel=%d) — should be model-x only (issue #87)", ai.ModelName, ai.ChannelID)
		}
	}

	// 组B（embeddings-group）：应只含 (渠道1, model-Y) 一条，不含 model-X。
	groupB := byName["embeddings-group"]
	if len(groupB.AutoItems) != 1 {
		t.Fatalf("embeddings-group AutoItems len = %d, want 1 (model-Y only)", len(groupB.AutoItems))
	}
	if groupB.AutoItems[0].ChannelID != 1 || groupB.AutoItems[0].ModelName != "model-y" {
		t.Fatalf("embeddings-group AutoItems[0] = (channel=%d, model=%q), want (1, model-y)", groupB.AutoItems[0].ChannelID, groupB.AutoItems[0].ModelName)
	}
}

// TestBuildGroupHealth_AutoItemsForNonAutoGroup 验证非 Auto 组同样填充本组范围内的
// 运行态快照（v2.8.10 行为变更）：成功率/延迟/首字 TTFT 对所有分组模式都在记录，
// 轮询等主力分组也需要在「分析中心」看到可见首字与疑似伪流式标记。过滤仍按本组
// (channel, model) 精确匹配，不会泄漏他组模型（issue #87）。
func TestBuildGroupHealth_AutoItemsForNonAutoGroup(t *testing.T) {
	groups := []model.Group{
		{
			ID:   1,
			Name: "round-robin-group",
			Mode: model.GroupModeRoundRobin,
			Items: []model.GroupItem{
				{ChannelID: 1, ModelName: "model-X"},
			},
		},
	}
	channelByID := map[int]model.Channel{
		1: {ID: 1, Name: "channelA", Enabled: true},
	}
	autoSnapshot := []balancer.AutoStatsSnapshotItem{
		{ChannelID: 1, ModelName: "model-x", SuccessRate: 0.9, SampleCount: 20, AvgLatencyMs: 260000, AvgTTFTMs: 259000, TTFTSamples: 3, LastActiveAt: time.Now()},
		{ChannelID: 1, ModelName: "model-other", SuccessRate: 0.9, SampleCount: 5, AvgLatencyMs: 100, LastActiveAt: time.Now()},
	}

	items := buildGroupHealth(groups, channelByID, nil, autoSnapshot, 10)
	if len(items) != 1 {
		t.Fatalf("expected 1 group health item, got %d", len(items))
	}
	if len(items[0].AutoItems) != 1 {
		t.Fatalf("non-Auto group AutoItems len = %d, want 1 (model-x only)", len(items[0].AutoItems))
	}
	ai := items[0].AutoItems[0]
	if ai.ModelName != "model-x" {
		t.Fatalf("non-Auto group AutoItems[0].ModelName = %q, want model-x (no cross-model leak)", ai.ModelName)
	}
	if ai.AvgTTFTMs != 259000 || ai.TTFTSamples != 3 {
		t.Fatalf("AutoItems[0] TTFT = (%f, %d), want (259000, 3)", ai.AvgTTFTMs, ai.TTFTSamples)
	}
}

// TestBuildAutoStrategyItems_MapsTTFT 验证 buildAutoStrategyItems 把 balancer 快照的
// TTFT EMA 与样本数映射到对外 model（前端据此渲染「可见首字」与疑似伪流式标记）。
func TestBuildAutoStrategyItems_MapsTTFT(t *testing.T) {
	snapshot := []balancer.AutoStatsSnapshotItem{
		{
			ChannelID:    97,
			ModelName:    "gemini-3.1-pro-preview",
			SuccessRate:  1,
			SampleCount:  4,
			AvgLatencyMs: 260379,
			AvgTTFTMs:    260112,
			TTFTSamples:  2,
		},
	}
	channelByID := map[int]model.Channel{
		97: {ID: 97, Name: "GG", Enabled: true},
	}

	items := buildAutoStrategyItems(snapshot, channelByID, 1)
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	if items[0].AvgTTFTMs != 260112 {
		t.Fatalf("AvgTTFTMs = %f, want 260112", items[0].AvgTTFTMs)
	}
	if items[0].TTFTSamples != 2 {
		t.Fatalf("TTFTSamples = %d, want 2", items[0].TTFTSamples)
	}
}

// TestFilterAutoSnapshot_PreciseMatch 验证 filterAutoSnapshot 按 (channel, model) 精确匹配，
// 模型名大小写归一化后比较。
func TestFilterAutoSnapshot_PreciseMatch(t *testing.T) {
	snapshot := []balancer.AutoStatsSnapshotItem{
		{ChannelID: 1, ModelName: "gpt-4o", SuccessRate: 0.9, SampleCount: 10},
		{ChannelID: 1, ModelName: "text-embedding-3", SuccessRate: 0.8, SampleCount: 10},
		{ChannelID: 2, ModelName: "gpt-4o", SuccessRate: 0.7, SampleCount: 10},
	}

	// scope 用原始大小写，应归一化后匹配小写快照名。
	scope := buildGroupAutoScope([]model.GroupItem{
		{ChannelID: 1, ModelName: "GPT-4O"},   // 大写
		{ChannelID: 2, ModelName: " gpt-4o "}, // 带空格
	})

	got := filterAutoSnapshot(snapshot, scope)
	if len(got) != 2 {
		t.Fatalf("filterAutoSnapshot len = %d, want 2 (both gpt-4o entries)", len(got))
	}
	for _, s := range got {
		if s.ModelName != "gpt-4o" {
			t.Fatalf("filterAutoSnapshot leaked model %q", s.ModelName)
		}
	}
}

// TestAnalyticsAutoStrategyGet_GroupScopedFiltersByChannelModel 是端到端回归测试：
// 调用 AnalyticsAutoStrategyGet(ctx, groupID) 时只返回该组 (channel, model) 条目。
// channel.List 与 group.GroupList 都走内存缓存，故直接注入缓存（与 group_test.go 同做法）。
func TestAnalyticsAutoStrategyGet_GroupScopedFiltersByChannelModel(t *testing.T) {
	// 注入渠道缓存：渠道1 同时在两个组、两个模型上有数据；渠道2 仅组A。
	chCache := channel.GetCache()
	chCache.Set(1, model.Channel{ID: 1, Name: "channelShared", Enabled: true})
	chCache.Set(2, model.Channel{ID: 2, Name: "channelB", Enabled: true})
	t.Cleanup(func() {
		chCache.Del(1)
		chCache.Del(2)
	})

	// 注入分组缓存：组A(chat, model-X) 含渠道1+2；组B(embeddings, model-Y) 仅渠道1。
	gCache := group.GetCache()
	gCache.Set(1, model.Group{
		ID: 1, Name: "chat-group", Mode: model.GroupModeAuto, EndpointType: "chat",
		Items: []model.GroupItem{
			{GroupID: 1, ChannelID: 1, ModelName: "model-X"},
			{GroupID: 1, ChannelID: 2, ModelName: "model-X"},
		},
	})
	gCache.Set(2, model.Group{
		ID: 2, Name: "embeddings-group", Mode: model.GroupModeAuto, EndpointType: "embeddings",
		Items: []model.GroupItem{
			{GroupID: 2, ChannelID: 1, ModelName: "model-Y"},
		},
	})
	t.Cleanup(func() {
		gCache.Del(1)
		gCache.Del(2)
	})

	// 通过公开 API 注入 Auto 快照数据（全局 sync.Map）。
	balancer.RecordAutoSuccess(1, "model-X")
	balancer.RecordAutoFailure(1, "model-X")
	balancer.RecordAutoSuccess(1, "model-Y")
	balancer.RecordAutoSuccess(2, "model-X")
	t.Cleanup(func() {
		balancer.RemoveChannelStats(1)
		balancer.RemoveChannelStats(2)
	})

	groupAID := 1
	itemsA, err := AnalyticsAutoStrategyGet(context.Background(), &groupAID)
	if err != nil {
		t.Fatalf("AnalyticsAutoStrategyGet(groupA) error: %v", err)
	}
	// 组A 应只含 model-X 的两条（渠道1 + 渠道2），不含 model-Y。
	if len(itemsA) != 2 {
		t.Fatalf("groupA items len = %d, want 2 (model-X only); got %+v", len(itemsA), itemsA)
	}
	for _, it := range itemsA {
		if it.ModelName != "model-x" {
			t.Fatalf("groupA leaked model %q (issue #87)", it.ModelName)
		}
	}

	groupBID := 2
	itemsB, err := AnalyticsAutoStrategyGet(context.Background(), &groupBID)
	if err != nil {
		t.Fatalf("AnalyticsAutoStrategyGet(groupB) error: %v", err)
	}
	// 组B 应只含 (渠道1, model-Y) 一条。
	if len(itemsB) != 1 {
		t.Fatalf("groupB items len = %d, want 1 (model-Y only); got %+v", len(itemsB), itemsB)
	}
	if itemsB[0].ChannelID != 1 || itemsB[0].ModelName != "model-y" {
		t.Fatalf("groupB item = (channel=%d, model=%q), want (1, model-y)", itemsB[0].ChannelID, itemsB[0].ModelName)
	}
}

// TestAnalyticsChannelModelBreakdownGet_LegacyLogWithoutAttempts 验证 issue #87 修复：
// relay_log_attempts 表存在时，没有 attempts 行的历史 relay_logs（issue #67 修复部署前
// 的日志）仍能被渠道×模型聚合覆盖，不会因只查 attempts 表而消失。
func TestAnalyticsChannelModelBreakdownGet_LegacyLogWithoutAttempts(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "analytics-legacy.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatalf("InitLogDB(shared) failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	if err := setting.SetString(model.SettingKeyRelayLogKeepEnabled, "true"); err != nil {
		t.Fatalf("enable relay log keep failed: %v", err)
	}

	// 历史日志：无 attempts 行（模拟 issue #67 修复前的数据），整体成功。
	legacyLog := model.RelayLog{
		ID: 9001, Time: time.Now().Unix(),
		RequestModelName: "gpt-4o", ActualModelName: "gpt-4o",
		ChannelId: 33, ChannelName: "legacyChannel", Error: "",
		InputTokens: 77, OutputTokens: 23, Cost: 0.5,
	}
	if err := db.GetLogDB().Create(&legacyLog).Error; err != nil {
		t.Fatalf("seed legacy relay log failed: %v", err)
	}
	// 故意不调用 RelayLogAttemptsAdd —— 模拟修复部署前的历史数据。

	restore := relaylog.SetCacheForTest(nil)
	t.Cleanup(restore)

	items, err := AnalyticsChannelModelBreakdownGet(context.Background(), model.AnalyticsRange7D, nil)
	if err != nil {
		t.Fatalf("AnalyticsChannelModelBreakdownGet error: %v", err)
	}

	// 历史日志应出现在渠道×模型聚合中（修复前会因只查 attempts 表而丢失）。
	var found bool
	for _, it := range items {
		if it.ChannelID == 33 && it.ModelName == "gpt-4o" {
			found = true
			if it.RequestCount != 1 || it.SuccessRate < 99.9 {
				t.Fatalf("legacy log request_count=%d success_rate=%f, want 1/~100", it.RequestCount, it.SuccessRate)
			}
			if it.InputTokens != 77 || it.OutputTokens != 23 {
				t.Fatalf("legacy log tokens in=%d out=%d, want 77/23", it.InputTokens, it.OutputTokens)
			}
		}
	}
	if !found {
		t.Fatalf("legacy log (no attempts row) missing from channel-model breakdown (issue #87)")
	}
}
