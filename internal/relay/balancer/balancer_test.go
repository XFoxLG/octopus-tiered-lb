package balancer

import (
	"fmt"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	ch "github.com/lingyuins/octopus/internal/op/channel"
)

func clearAutoStatsForTest() {
	globalAutoStats.Range(func(key, _ any) bool {
		globalAutoStats.Delete(key)
		return true
	})
}

func recordOutcome(channelID int, modelName string, success bool, count int) {
	for i := 0; i < count; i++ {
		stats := getOrCreateStats(channelID, modelName)
		stats.Record(success)
	}
}

func TestAutoCandidatesPreferLowerSampleCountDuringExploration(t *testing.T) {
	clearAutoStatsForTest()

	modelName := fmt.Sprintf("auto-explore-%d", time.Now().UnixNano())
	items := []model.GroupItem{
		{ChannelID: 1, ModelName: modelName, Weight: 100, Priority: 1},
		{ChannelID: 2, ModelName: modelName, Weight: 1, Priority: 2},
	}

	recordOutcome(1, modelName, true, 1)

	got := (&Auto{}).Candidates(items)
	if len(got) != 2 {
		t.Fatalf("Candidates() len = %d, want 2", len(got))
	}
	if got[0].ChannelID != 2 {
		t.Fatalf("Candidates()[0].ChannelID = %d, want 2", got[0].ChannelID)
	}
}

func TestAutoCandidatesUseWeightPriorityWhenAllSamplesAreZero(t *testing.T) {
	clearAutoStatsForTest()

	modelName := fmt.Sprintf("auto-zero-%d", time.Now().UnixNano())
	items := []model.GroupItem{
		{ChannelID: 1, ModelName: modelName, Weight: 1, Priority: 2},
		{ChannelID: 2, ModelName: modelName, Weight: 10, Priority: 1},
	}

	got := (&Auto{}).Candidates(items)
	if len(got) != 2 {
		t.Fatalf("Candidates() len = %d, want 2", len(got))
	}
	if got[0].ChannelID != 2 {
		t.Fatalf("Candidates()[0].ChannelID = %d, want 2", got[0].ChannelID)
	}
}

func TestAutoCandidatesPreferHigherSuccessRateAfterMinSamples(t *testing.T) {
	clearAutoStatsForTest()

	modelName := fmt.Sprintf("auto-exploit-%d", time.Now().UnixNano())
	items := []model.GroupItem{
		{ChannelID: 1, ModelName: modelName, Weight: 1, Priority: 1},
		{ChannelID: 2, ModelName: modelName, Weight: 100, Priority: 2},
	}

	recordOutcome(1, modelName, true, 10)
	recordOutcome(2, modelName, true, 6)
	recordOutcome(2, modelName, false, 4)

	got := (&Auto{}).Candidates(items)
	if len(got) != 2 {
		t.Fatalf("Candidates() len = %d, want 2", len(got))
	}
	if got[0].ChannelID != 1 {
		t.Fatalf("Candidates()[0].ChannelID = %d, want 1", got[0].ChannelID)
	}
}

func TestAutoCandidatesUseWeightPriorityAsTieBreaker(t *testing.T) {
	clearAutoStatsForTest()

	modelName := fmt.Sprintf("auto-tie-%d", time.Now().UnixNano())
	items := []model.GroupItem{
		{ChannelID: 1, ModelName: modelName, Weight: 1, Priority: 2},
		{ChannelID: 2, ModelName: modelName, Weight: 10, Priority: 1},
	}

	recordOutcome(1, modelName, true, 10)
	recordOutcome(2, modelName, true, 10)

	// B2 改动后语义：两渠道评分相同（各 10 条全成功）→ 落入同一分桶被随机打散，
	// 不再保证确定性 weight tie-breaker 排序（这正是修复「轮流垄断」的目的）。
	// 本测试改为断言集合完整性 + 分桶外场景的 tie-breaker 仍生效（见
	// TestAutoCandidatesScoreGapKeepsDeterministicOrder 中的大分差排序）。
	got := (&Auto{}).Candidates(items)
	if len(got) != 2 {
		t.Fatalf("Candidates() len = %d, want 2", len(got))
	}
	seen := map[int]bool{got[0].ChannelID: true, got[1].ChannelID: true}
	if !seen[1] || !seen[2] {
		t.Fatalf("Candidates() = [%d, %d], want both channels present (bucket shuffle may randomize order)", got[0].ChannelID, got[1].ChannelID)
	}
}

// TestAutoCandidatesScoreGapKeepsDeterministicOrder 验证分差超出桶容差时排序确定：
// 渠道 1 成功率 100%、渠道 2 成功率 60%（分差 0.28 >> 默认容差 0.05），
// 多轮调用顺序恒为 [1, 2]，不受桶打散影响。
func TestAutoCandidatesScoreGapKeepsDeterministicOrder(t *testing.T) {
	clearAutoStatsForTest()

	modelName := fmt.Sprintf("auto-gap-%d", time.Now().UnixNano())
	items := []model.GroupItem{
		{ChannelID: 1, ModelName: modelName, Weight: 1, Priority: 2},
		{ChannelID: 2, ModelName: modelName, Weight: 10, Priority: 1},
	}

	recordOutcome(1, modelName, true, 10)
	recordOutcome(2, modelName, true, 6)
	recordOutcome(2, modelName, false, 4)

	for round := 0; round < 50; round++ {
		got := (&Auto{}).Candidates(items)
		if len(got) != 2 {
			t.Fatalf("round %d: Candidates() len = %d, want 2", round, len(got))
		}
		if got[0].ChannelID != 1 {
			t.Fatalf("round %d: Candidates()[0].ChannelID = %d, want 1 (score gap 0.28 exceeds bucket tolerance, order must be deterministic)", round, got[0].ChannelID)
		}
	}
}

// TestWeightedCandidatesFollowWeightRatio 锁住 2026-10-01 修复的回归：加权分配曾经在
// 大重构后退化成「按权重降序排序」，导致权重最高的渠道独占全部流量。候选列表的首位
// 必须是加权随机抽取，而不是固定的权重最大项。
func TestWeightedCandidatesFollowWeightRatio(t *testing.T) {
	items := []model.GroupItem{
		{ChannelID: 1, ModelName: "weighted-ratio", Weight: 9},
		{ChannelID: 2, ModelName: "weighted-ratio", Weight: 1},
	}

	const draws = 20000
	firstCounts := map[int]int{}
	for i := 0; i < draws; i++ {
		got := (&Weighted{}).Candidates(items)
		if len(got) != 2 {
			t.Fatalf("Candidates() len = %d, want 2", len(got))
		}
		firstCounts[got[0].ChannelID]++
	}

	// 期望 90% / 10%；给足容差以免偶发波动把测试变成 flaky。
	ratio := float64(firstCounts[1]) / float64(draws)
	if ratio < 0.85 || ratio > 0.94 {
		t.Fatalf("channel 1 first-pick ratio = %.4f, want ~0.90 (counts=%v)", ratio, firstCounts)
	}
	if firstCounts[2] == 0 {
		t.Fatalf("channel 2 never picked first; weighted randomness is not applied (counts=%v)", firstCounts)
	}
}

// TestWeightedCandidatesRemainAFullPermutation 保证加权随机只是换顺序，不会丢候选或
// 重复候选——调用方仍依赖完整列表做故障转移重试。
func TestWeightedCandidatesRemainAFullPermutation(t *testing.T) {
	items := []model.GroupItem{
		{ChannelID: 1, ModelName: "perm", Weight: 5},
		{ChannelID: 2, ModelName: "perm", Weight: 3},
		{ChannelID: 3, ModelName: "perm", Weight: 0},
	}

	for i := 0; i < 500; i++ {
		got := (&Weighted{}).Candidates(items)
		if len(got) != 3 {
			t.Fatalf("Candidates() len = %d, want 3", len(got))
		}
		seen := map[int]bool{}
		for _, item := range got {
			if seen[item.ChannelID] {
				t.Fatalf("duplicate candidate channel %d in %v", item.ChannelID, got)
			}
			seen[item.ChannelID] = true
		}
		// 零权重按 1 处理，因此仍然必须出现。
		if !seen[3] {
			t.Fatalf("zero-weight channel dropped from candidates: %v", got)
		}
	}
}

func TestIteratorForwardedAttemptsExcludesSkippedAndCircuitBreak(t *testing.T) {
	it := &Iterator{
		attempts: []model.ChannelAttempt{
			{Status: model.AttemptSkipped},
			{Status: model.AttemptCircuitBreak},
			{Status: model.AttemptFailed},
			{Status: model.AttemptSuccess},
		},
	}

	if got := it.ForwardedAttempts(); got != 2 {
		t.Fatalf("ForwardedAttempts() = %d, want 2", got)
	}
}

func TestDisposableChannelsSortedFirst(t *testing.T) {
	// Disposable channels should be sorted before non-disposable ones,
	// regardless of the underlying balancer strategy.
	disposableSet := map[int]bool{3: true, 5: true}
	orig := DisposableChannelFunc
	DisposableChannelFunc = func(channelID int) bool {
		return disposableSet[channelID]
	}
	defer func() { DisposableChannelFunc = orig }()

	items := []model.GroupItem{
		{ChannelID: 1, ModelName: "gpt-4", Priority: 1, Weight: 10},
		{ChannelID: 3, ModelName: "gpt-4", Priority: 5, Weight: 1},
		{ChannelID: 2, ModelName: "gpt-4", Priority: 2, Weight: 5},
		{ChannelID: 5, ModelName: "gpt-4", Priority: 3, Weight: 3},
	}

	group := model.Group{Mode: model.GroupModeRoundRobin, Items: items}
	it := NewIterator(group, 0, "gpt-4", nil)

	// First two candidates should be the disposable channels (3 and 5)
	seen := make(map[int]bool)
	for i := 0; i < 2; i++ {
		if !it.Next() {
			t.Fatalf("Next() returned false at index %d", i)
		}
		chID := it.Item().ChannelID
		if !disposableSet[chID] {
			t.Fatalf("candidate[%d].ChannelID = %d, expected a disposable channel", i, chID)
		}
		seen[chID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 distinct disposable channels in first 2 positions, got %d", len(seen))
	}
}

func TestDisposableChannelsRespectsSticky(t *testing.T) {
	// Sticky channel should take precedence over disposable priority.
	disposableSet := map[int]bool{3: true}
	orig := DisposableChannelFunc
	DisposableChannelFunc = func(channelID int) bool {
		return disposableSet[channelID]
	}
	defer func() { DisposableChannelFunc = orig }()

	items := []model.GroupItem{
		{ChannelID: 1, ModelName: "gpt-4", Priority: 1, Weight: 10},
		{ChannelID: 3, ModelName: "gpt-4", Priority: 5, Weight: 1},
	}

	// Set up sticky: apiKeyID=42, model=gpt-4 -> channel 1 (non-disposable)
	SetSticky(42, "gpt-4", 1, 0)
	defer globalSession.Delete(sessionKey(42, "gpt-4"))

	group := model.Group{Mode: model.GroupModeRoundRobin, SessionKeepTime: 300, Items: items}
	it := NewIterator(group, 42, "gpt-4", nil)

	if !it.Next() {
		t.Fatal("Next() returned false")
	}
	if it.Item().ChannelID != 1 {
		t.Fatalf("first candidate ChannelID = %d, want 1 (sticky, non-disposable)", it.Item().ChannelID)
	}
}

// seedChannelWithKeys 向 channel cache 注入一个带指定 key 的渠道，供熔断聚合查询使用。
func seedChannelWithKeys(channelID int, keyIDs []int) {
	keys := make([]model.ChannelKey, len(keyIDs))
	for i, id := range keyIDs {
		keys[i] = model.ChannelKey{ID: id, ChannelID: channelID, Enabled: true, ChannelKey: fmt.Sprintf("key-%d", id)}
	}
	ch.GetCache().Set(channelID, model.Channel{ID: channelID, Keys: keys})
}

// tripKey 对单个 key 连续记录失败直到触发熔断（默认阈值 5）。
func tripKey(channelID, keyID int, modelName string) {
	for i := 0; i < 5; i++ {
		RecordFailure(channelID, keyID, modelName)
	}
}

// TestAutoCandidatesDeprioritizeAllKeysTrippedChannel 验证 #133：两个渠道样本与成功率相同，
// 其中一个全熔断，应被降权排到末尾（不剔除）。
func TestAutoCandidatesDeprioritizeAllKeysTrippedChannel(t *testing.T) {
	clearAutoStatsForTest()
	clearCircuitBreakerForTest()

	modelName := fmt.Sprintf("auto-tripped-%d", time.Now().UnixNano())
	// 渠道 1（全熔断）和渠道 2（健康）各有一个 key
	seedChannelWithKeys(1, []int{11})
	seedChannelWithKeys(2, []int{22})
	defer func() {
		ch.GetCache().Del(1, 2)
	}()

	items := []model.GroupItem{
		{ChannelID: 1, ModelName: modelName, Weight: 100, Priority: 1},
		{ChannelID: 2, ModelName: modelName, Weight: 1, Priority: 2},
	}

	// 两渠道成功率相同（均达到 minSamples），渠道 1 权重更高--无熔断时应排第一
	recordOutcome(1, modelName, true, 10)
	recordOutcome(2, modelName, true, 10)

	// 熔断渠道 1 的唯一 key
	tripKey(1, 11, modelName)

	got := (&Auto{}).Candidates(items)
	if len(got) != 2 {
		t.Fatalf("Candidates() len = %d, want 2", len(got))
	}
	if got[0].ChannelID != 2 {
		t.Fatalf("Candidates()[0].ChannelID = %d, want 2 (channel 1 is fully tripped, should be deprioritized)", got[0].ChannelID)
	}
	if got[1].ChannelID != 1 {
		t.Fatalf("Candidates()[1].ChannelID = %d, want 1 (tripped channel should be kept but sorted last)", got[1].ChannelID)
	}
}

// TestAutoCandidatesKeepPartialTrippedChannelNormal 验证 #133：渠道只有部分 key 熔断时
// 不应被降权（"全熔断才降权"语义），正常参与排序。
func TestAutoCandidatesKeepPartialTrippedChannelNormal(t *testing.T) {
	clearAutoStatsForTest()
	clearCircuitBreakerForTest()

	modelName := fmt.Sprintf("auto-partial-%d", time.Now().UnixNano())
	// 渠道 1 有两个 key，只熔断其中一个；渠道 2 健康
	seedChannelWithKeys(1, []int{11, 12})
	seedChannelWithKeys(2, []int{22})
	defer func() {
		ch.GetCache().Del(1, 2)
	}()

	items := []model.GroupItem{
		{ChannelID: 1, ModelName: modelName, Weight: 100, Priority: 1},
		{ChannelID: 2, ModelName: modelName, Weight: 1, Priority: 2},
	}

	// 两渠道成功率相同，渠道 1 权重更高--应排第一（部分熔断不降权）
	recordOutcome(1, modelName, true, 10)
	recordOutcome(2, modelName, true, 10)

	// 只熔断渠道 1 的 key 11，key 12 仍健康 -> 未全熔断
	tripKey(1, 11, modelName)

	got := (&Auto{}).Candidates(items)
	if len(got) != 2 {
		t.Fatalf("Candidates() len = %d, want 2", len(got))
	}
	// B2 改动后语义：两渠道评分相同 → 同分桶打散，顺序不再确定。
	// 本测试关注「部分熔断不降权」（-Inf 惩罚未触发），改为断言两渠道评分排序
	// 均在前两位且渠道 1 未被降权到末尾：即 50/50 轮询下渠道 1 仍有机会排第一。
	rankedFirst := map[int]bool{}
	for round := 0; round < 50; round++ {
		got := (&Auto{}).Candidates(items)
		if len(got) != 2 {
			t.Fatalf("round %d: Candidates() len = %d, want 2", round, len(got))
		}
		rankedFirst[got[0].ChannelID] = true
	}
	if !rankedFirst[1] {
		t.Fatalf("channel 1 never ranked first over 50 rounds: partial tripped channel should NOT be deprioritized (want bucket shuffle to still let it lead)")
	}
}
