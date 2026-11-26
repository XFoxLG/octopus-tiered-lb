package model

import (
	"testing"
)

// stubKeyAvailabilityScore 用于测试 selectKeyByAvailability，返回预设分数。
var stubKeyAvailabilityScores map[string]float64

func stubKeyAvailabilityScoreFunc(channelID, keyID int, modelName string) float64 {
	k := availabilityStubKey(channelID, keyID, modelName)
	if s, ok := stubKeyAvailabilityScores[k]; ok {
		return s
	}
	return keyAvailabilityMaxScore
}

func availabilityStubKey(channelID, keyID int, modelName string) string {
	return formatAvailabilityStubKey(channelID, keyID, modelName)
}

// formatAvailabilityStubKey 与 balancer.availabilityKey 格式一致，避免跨包依赖。
func formatAvailabilityStubKey(channelID, keyID int, modelName string) string {
	return intToStr(channelID) + ":" + intToStr(keyID) + ":" + trimSpace(modelName)
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

const keyAvailabilityMaxScore = 100.0

func setupAvailabilityTest() func() {
	oldFunc := KeyAvailabilityScoreFunc
	oldStrategy := GlobalKeySelectionStrategyFunc
	KeyAvailabilityScoreFunc = stubKeyAvailabilityScoreFunc
	GlobalKeySelectionStrategyFunc = func() string { return "availability" }
	stubKeyAvailabilityScores = make(map[string]float64)
	return func() {
		KeyAvailabilityScoreFunc = oldFunc
		GlobalKeySelectionStrategyFunc = oldStrategy
		stubKeyAvailabilityScores = nil
	}
}

func setupCostTest() func() {
	oldAvailabilityFunc := KeyAvailabilityScoreFunc
	oldSpeedFunc := KeySpeedTPSFunc
	oldStrategy := GlobalKeySelectionStrategyFunc
	oldCooldownFunc := KeyCooldownFunc
	KeyAvailabilityScoreFunc = nil
	KeySpeedTPSFunc = nil
	GlobalKeySelectionStrategyFunc = func() string { return "cost" }
	KeyCooldownFunc = nil
	return func() {
		KeyAvailabilityScoreFunc = oldAvailabilityFunc
		KeySpeedTPSFunc = oldSpeedFunc
		GlobalKeySelectionStrategyFunc = oldStrategy
		KeyCooldownFunc = oldCooldownFunc
	}
}

func makeChannelWithKeys(keys ...ChannelKey) *Channel {
	return &Channel{
		ID:   1,
		Name: "test-channel",
		Keys: keys,
	}
}

func enabledKey(id int, cost float64) ChannelKey {
	return ChannelKey{
		ID:         id,
		ChannelID:  1,
		Enabled:    true,
		ChannelKey: "sk-test-" + intToStr(id),
		TotalCost:  cost,
	}
}

func TestSelectKeyByAvailabilityPicksHighestScore(t *testing.T) {
	cleanup := setupAvailabilityTest()
	defer cleanup()

	ch := makeChannelWithKeys(
		enabledKey(1, 1.0),
		enabledKey(2, 2.0),
		enabledKey(3, 3.0),
	)
	// key 1 出错降到 50，key 2 满分，key 3 降到 30
	stubKeyAvailabilityScores[availabilityStubKey(1, 1, "gpt-4o")] = 50
	stubKeyAvailabilityScores[availabilityStubKey(1, 2, "gpt-4o")] = 100
	stubKeyAvailabilityScores[availabilityStubKey(1, 3, "gpt-4o")] = 30

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 2 {
		t.Fatalf("expected key 2 (highest score), got key %d", key.ID)
	}
}

func TestSelectKeyByAvailabilityTiePicksFirstInArray(t *testing.T) {
	cleanup := setupAvailabilityTest()
	defer cleanup()

	// 所有 key 满分（初始状态），应选数组第一个
	ch := makeChannelWithKeys(
		enabledKey(1, 5.0), // 成本最高，但可用度同分应优先
		enabledKey(2, 1.0), // 成本最低
	)

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 1 {
		t.Fatalf("expected key 1 (first in array on tie), got key %d", key.ID)
	}
}

func TestSelectKeyByAvailabilityAllZeroFallsBackToCost(t *testing.T) {
	cleanup := setupAvailabilityTest()
	defer cleanup()

	ch := makeChannelWithKeys(
		enabledKey(1, 5.0), // 成本高
		enabledKey(2, 1.0), // 成本低
	)
	// 所有 key 分数 ≤ 0
	stubKeyAvailabilityScores[availabilityStubKey(1, 1, "gpt-4o")] = 0
	stubKeyAvailabilityScores[availabilityStubKey(1, 2, "gpt-4o")] = 0

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 2 {
		t.Fatalf("expected key 2 (lowest cost fallback), got key %d", key.ID)
	}
}

func TestSelectKeyByCostWhenStrategyIsCost(t *testing.T) {
	cleanup := setupAvailabilityTest()
	defer cleanup()
	// 覆盖为 cost 策略
	GlobalKeySelectionStrategyFunc = func() string { return "cost" }

	ch := makeChannelWithKeys(
		enabledKey(1, 5.0), // 成本高
		enabledKey(2, 1.0), // 成本低
	)
	// 即使 key 1 可用度更高，cost 策略应选成本最低的 key 2
	stubKeyAvailabilityScores[availabilityStubKey(1, 1, "gpt-4o")] = 100
	stubKeyAvailabilityScores[availabilityStubKey(1, 2, "gpt-4o")] = 10

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 2 {
		t.Fatalf("expected key 2 (lowest cost), got key %d", key.ID)
	}
}

func TestChannelStrategyOverridesGlobal(t *testing.T) {
	cleanup := setupAvailabilityTest()
	defer cleanup()
	GlobalKeySelectionStrategyFunc = func() string { return "cost" }

	// 渠道显式设为 availability，覆盖全局 cost
	ch := makeChannelWithKeys(
		enabledKey(1, 1.0),
		enabledKey(2, 2.0),
	)
	ch.KeySelectionStrategy = "availability"
	stubKeyAvailabilityScores[availabilityStubKey(1, 1, "gpt-4o")] = 30
	stubKeyAvailabilityScores[availabilityStubKey(1, 2, "gpt-4o")] = 90

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 2 {
		t.Fatalf("expected key 2 (channel override to availability), got key %d", key.ID)
	}
}

func TestSelectKeyByAvailabilitySkipsZeroScore(t *testing.T) {
	cleanup := setupAvailabilityTest()
	defer cleanup()

	ch := makeChannelWithKeys(
		enabledKey(1, 1.0),
		enabledKey(2, 2.0),
	)
	// key 1 分数为 0（被 401 惩罚），key 2 满分
	stubKeyAvailabilityScores[availabilityStubKey(1, 1, "gpt-4o")] = 0
	stubKeyAvailabilityScores[availabilityStubKey(1, 2, "gpt-4o")] = 100

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 2 {
		t.Fatalf("expected key 2 (key 1 has zero score), got key %d", key.ID)
	}
}

func setupPriorityTest() func() {
	oldAvailabilityFunc := KeyAvailabilityScoreFunc
	oldStrategy := GlobalKeySelectionStrategyFunc
	oldCooldownFunc := KeyCooldownFunc
	KeyAvailabilityScoreFunc = nil
	GlobalKeySelectionStrategyFunc = func() string { return "priority" }
	KeyCooldownFunc = nil
	return func() {
		KeyAvailabilityScoreFunc = oldAvailabilityFunc
		GlobalKeySelectionStrategyFunc = oldStrategy
		KeyCooldownFunc = oldCooldownFunc
	}
}

func priorityKey(id int, priority int, cost float64) ChannelKey {
	k := enabledKey(id, cost)
	k.Priority = priority
	return k
}

func TestSelectKeyByPriorityPicksHighestPriority(t *testing.T) {
	cleanup := setupPriorityTest()
	defer cleanup()

	ch := makeChannelWithKeys(
		priorityKey(1, 20, 1.0),
		priorityKey(2, 10, 5.0),
		priorityKey(3, 30, 0.1),
	)

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 3 {
		t.Fatalf("expected key 3 (highest priority value), got key %d", key.ID)
	}
}

func TestSelectKeyByPriorityTieFallsBackToLowestCost(t *testing.T) {
	cleanup := setupPriorityTest()
	defer cleanup()

	ch := makeChannelWithKeys(
		priorityKey(1, 10, 5.0),
		priorityKey(2, 10, 1.0),
	)

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 2 {
		t.Fatalf("expected key 2 (same priority, lowest cost), got key %d", key.ID)
	}
}

func TestSelectKeyByPrioritySkipsExcludedDisabledEmptyAndCooldown(t *testing.T) {
	cleanup := setupPriorityTest()
	defer cleanup()
	KeyCooldownFunc = func(channelID, keyID int, modelName string) bool {
		return channelID == 1 && keyID == 3 && modelName == "gpt-4o"
	}

	disabled := priorityKey(1, 1, 0.1)
	disabled.Enabled = false
	empty := priorityKey(2, 2, 0.1)
	empty.ChannelKey = ""
	cooldown := priorityKey(3, 3, 0.1)
	excluded := priorityKey(4, 4, 0.1)
	eligible := priorityKey(5, 5, 0.1)
	ch := makeChannelWithKeys(disabled, empty, cooldown, excluded, eligible)

	key := ch.GetChannelKeyExcludingWithCooldown([]int{4}, "gpt-4o", 300)
	if key.ID != 5 {
		t.Fatalf("expected key 5 (first eligible priority candidate), got key %d", key.ID)
	}
}

func TestChannelInheritsGlobalPriorityStrategy(t *testing.T) {
	cleanup := setupPriorityTest()
	defer cleanup()

	ch := makeChannelWithKeys(
		priorityKey(1, 20, 1.0),
		priorityKey(2, 10, 5.0),
	)
	ch.KeySelectionStrategy = ""

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 1 {
		t.Fatalf("expected key 1 (global priority strategy, highest priority), got key %d", key.ID)
	}
}

// --- speed strategy tests (issue #140) ---

var stubKeyTPSScores map[string]float64

func stubKeyTPSFunc(channelID, keyID int, modelName string) float64 {
	k := formatAvailabilityStubKey(channelID, keyID, modelName)
	if s, ok := stubKeyTPSScores[k]; ok {
		return s
	}
	return 0
}

func setupSpeedTest() func() {
	oldFunc := KeySpeedTPSFunc
	oldStrategy := GlobalKeySelectionStrategyFunc
	KeySpeedTPSFunc = stubKeyTPSFunc
	GlobalKeySelectionStrategyFunc = func() string { return "speed" }
	stubKeyTPSScores = make(map[string]float64)
	return func() {
		KeySpeedTPSFunc = oldFunc
		GlobalKeySelectionStrategyFunc = oldStrategy
		stubKeyTPSScores = nil
	}
}

func TestSelectKeyBySpeedPicksHighestTPS(t *testing.T) {
	cleanup := setupSpeedTest()
	defer cleanup()

	ch := makeChannelWithKeys(
		enabledKey(1, 5.0), // cost 5.0
		enabledKey(2, 3.0), // cost 3.0
		enabledKey(3, 1.0), // cost 1.0 (lowest cost)
	)
	// key 1: 50 tps, key 2: 100 tps (fastest), key 3: 30 tps
	stubKeyTPSScores[availabilityStubKey(1, 1, "gpt-4o")] = 50
	stubKeyTPSScores[availabilityStubKey(1, 2, "gpt-4o")] = 100
	stubKeyTPSScores[availabilityStubKey(1, 3, "gpt-4o")] = 30

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 2 {
		t.Fatalf("expected key 2 (highest tps), got key %d", key.ID)
	}
}

func TestSelectKeyBySpeedTiePicksFirstInArray(t *testing.T) {
	cleanup := setupSpeedTest()
	defer cleanup()

	ch := makeChannelWithKeys(
		enabledKey(1, 5.0),
		enabledKey(2, 3.0),
	)
	// Both have same TPS → first in array wins
	stubKeyTPSScores[availabilityStubKey(1, 1, "gpt-4o")] = 100
	stubKeyTPSScores[availabilityStubKey(1, 2, "gpt-4o")] = 100

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 1 {
		t.Fatalf("expected key 1 (first in array on tie), got key %d", key.ID)
	}
}

func TestSelectKeyBySpeedAllZeroFallsBackToCost(t *testing.T) {
	cleanup := setupSpeedTest()
	defer cleanup()

	ch := makeChannelWithKeys(
		enabledKey(1, 5.0), // cost 5.0
		enabledKey(2, 1.0), // cost 1.0 (lowest cost)
	)
	// All zero TPS (cold start) → fall back to cost
	stubKeyTPSScores[availabilityStubKey(1, 1, "gpt-4o")] = 0
	stubKeyTPSScores[availabilityStubKey(1, 2, "gpt-4o")] = 0

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 2 {
		t.Fatalf("expected key 2 (lowest cost fallback), got key %d", key.ID)
	}
}

func TestSelectKeyBySpeedSkipsZeroTPS(t *testing.T) {
	cleanup := setupSpeedTest()
	defer cleanup()

	ch := makeChannelWithKeys(
		enabledKey(1, 1.0), // cost 1.0 (lowest), but 0 tps
		enabledKey(2, 5.0), // cost 5.0, but 50 tps (only one with data)
	)
	stubKeyTPSScores[availabilityStubKey(1, 1, "gpt-4o")] = 0
	stubKeyTPSScores[availabilityStubKey(1, 2, "gpt-4o")] = 50

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 2 {
		t.Fatalf("expected key 2 (only one with tps data), got key %d", key.ID)
	}
}

func TestSelectKeyByCostWhenStrategyIsSpeedButNoFunc(t *testing.T) {
	cleanup := setupSpeedTest()
	defer cleanup()
	// Override to speed but no func injected
	KeySpeedTPSFunc = nil

	ch := makeChannelWithKeys(
		enabledKey(1, 5.0),
		enabledKey(2, 1.0), // cost 1.0 (lowest)
	)
	stubKeyTPSScores[availabilityStubKey(1, 1, "gpt-4o")] = 100
	stubKeyTPSScores[availabilityStubKey(1, 2, "gpt-4o")] = 10

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 2 {
		t.Fatalf("expected key 2 (lowest cost, no speed func), got key %d", key.ID)
	}
}

func TestChannelStrategyOverridesGlobalSpeed(t *testing.T) {
	cleanup := setupSpeedTest()
	defer cleanup()

	ch := makeChannelWithKeys(
		enabledKey(1, 5.0),
		enabledKey(2, 1.0), // cost 1.0 (lowest)
	)
	ch.KeySelectionStrategy = "cost"                            // override global speed
	stubKeyTPSScores[availabilityStubKey(1, 1, "gpt-4o")] = 100 // fast
	stubKeyTPSScores[availabilityStubKey(1, 2, "gpt-4o")] = 10  // slow

	key := ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 2 {
		t.Fatalf("expected key 2 (channel overrides to cost), got key %d", key.ID)
	}
}

// TestKeySupportedModelsFiltersByModel 验证 key 的 SupportedModels 字段在
// 选 key 时按模型过滤：只有声明了该模型的 key 才会被选中。
func TestKeySupportedModelsFiltersByModel(t *testing.T) {
	cleanup := setupCostTest()
	defer cleanup()

	keyA := enabledKey(1, 1.0)
	keyA.SupportedModels = "gpt-4o,claude-3.5-sonnet"
	keyB := enabledKey(2, 2.0)
	keyB.SupportedModels = "step-3.7-flash,grok-3"
	ch := makeChannelWithKeys(keyA, keyB)

	// 请求 step-3.7-flash → 只能选 keyB
	key := ch.GetChannelKeyWithCooldown("step-3.7-flash", 300)
	if key.ID != 2 {
		t.Fatalf("expected key 2 (supports step-3.7-flash), got key %d", key.ID)
	}

	// 请求 gpt-4o → 只能选 keyA
	key = ch.GetChannelKeyWithCooldown("gpt-4o", 300)
	if key.ID != 1 {
		t.Fatalf("expected key 1 (supports gpt-4o), got key %d", key.ID)
	}
}

func TestAutoSyncKeyModelCatalogPrefersListedKeysAndFallsBackToUnknown(t *testing.T) {
	cleanup := setupCostTest()
	defer cleanup()

	unlisted := enabledKey(1, 1.0)
	unlisted.SupportedModels = "other-model"
	unknown := enabledKey(3, 0.1)
	listed := enabledKey(2, 10.0)
	listed.SupportedModels = "target-model"
	ch := makeChannelWithKeys(unlisted, unknown, listed)
	ch.AutoSyncKeyModels = true

	key := ch.GetChannelKeyWithCooldown("target-model", 300)
	if key.ID != listed.ID {
		t.Fatalf("selected key %d, want listed key %d while a positive catalog match exists", key.ID, listed.ID)
	}

	// If no enabled key lists the explicitly configured model, catalog absence
	// is uncertain for an auto-sync channel, so the route may still be attempted.
	// This includes keys whose catalog is empty; they must not outrank an explicit match.
	ch.Keys = []ChannelKey{unlisted, unknown}
	key = ch.GetChannelKeyWithCooldown("target-model", 300)
	if key.ID != unknown.ID {
		t.Fatalf("selected key %d, want lowest-cost unknown-capability key %d as fallback", key.ID, unknown.ID)
	}
}

// TestKeySupportedModelsEmptyMeansNoRestriction 验证空 SupportedModels
// 表示不限制（兼容存量 key）：任何模型都可选中该 key。
func TestKeySupportedModelsEmptyMeansNoRestriction(t *testing.T) {
	cleanup := setupCostTest()
	defer cleanup()

	keyA := enabledKey(1, 1.0)
	// keyA 不设 SupportedModels（空=不限）
	keyB := enabledKey(2, 2.0)
	keyB.SupportedModels = "step-3.7-flash"
	ch := makeChannelWithKeys(keyA, keyB)

	// 请求 grok-3：keyB 不支持但 keyA 不限 → 选 cost 最低的 keyA
	key := ch.GetChannelKeyWithCooldown("grok-3", 300)
	if key.ID != 1 {
		t.Fatalf("expected key 1 (empty=unrestricted, lowest cost), got key %d", key.ID)
	}

	// 请求 step-3.7-flash：两把 key 都可选（keyA 不限、keyB 支持），选 cost 最低 keyA
	key = ch.GetChannelKeyWithCooldown("step-3.7-flash", 300)
	if key.ID != 1 {
		t.Fatalf("expected key 1 (lowest cost among eligible), got key %d", key.ID)
	}
}

// TestKeySupportedModelsRetryExcludesUnsupportedKey 验证重试场景：
// 当某 key 对某模型触发失败被排除后，不会重选到不支持该模型的 key。
func TestKeySupportedModelsRetryExcludesUnsupportedKey(t *testing.T) {
	cleanup := setupCostTest()
	defer cleanup()

	keyA := enabledKey(1, 1.0)
	keyA.SupportedModels = "gpt-4o"
	keyB := enabledKey(2, 2.0)
	keyB.SupportedModels = "step-3.7-flash"
	ch := makeChannelWithKeys(keyA, keyB)

	// 请求 step-3.7-flash：排除 keyB（模拟已失败），keyA 不支持该模型 → 返回空
	key := ch.GetChannelKeyExcludingWithCooldown([]int{2}, "step-3.7-flash", 300)
	if key.ID != 0 {
		t.Fatalf("expected empty key (no eligible key left), got key %d", key.ID)
	}

	// 请求 gpt-4o：排除 keyA，keyB 不支持该模型 → 返回空
	key = ch.GetChannelKeyExcludingWithCooldown([]int{1}, "gpt-4o", 300)
	if key.ID != 0 {
		t.Fatalf("expected empty key (no eligible key left), got key %d", key.ID)
	}
}

// TestKeySupportedModelsEmptyModelNameSkipsFilter 验证 modelName 为空时
// 不做模型过滤（后台任务场景）。
func TestKeySupportedModelsEmptyModelNameSkipsFilter(t *testing.T) {
	cleanup := setupCostTest()
	defer cleanup()

	keyA := enabledKey(1, 1.0)
	keyA.SupportedModels = "gpt-4o"
	keyB := enabledKey(2, 2.0)
	keyB.SupportedModels = "step-3.7-flash"
	ch := makeChannelWithKeys(keyA, keyB)

	// modelName 为空 → 不过滤，选 cost 最低的 keyA
	key := ch.GetChannelKeyWithCooldown("", 300)
	if key.ID != 1 {
		t.Fatalf("expected key 1 (empty model=skip filter, lowest cost), got key %d", key.ID)
	}
}
