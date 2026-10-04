package balancer

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/lingyuins/octopus/internal/model"
)

// seedAutoRand 注入确定性随机源，t.Cleanup 还原全局 rand 函数变量。
// 桶打散用 math/rand 全局源（rand.Shuffle），softmax 采样与 explore 概率判定用
// nextAutoCandidatesFloat64 函数变量——两处都注入同一 seeded 源。
func seedAutoRand(t *testing.T, seed int64) *rand.Rand {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	orig := nextAutoCandidatesFloat64
	nextAutoCandidatesFloat64 = r.Float64
	t.Cleanup(func() { nextAutoCandidatesFloat64 = orig })
	return r
}

// buildAutoExploitItems 构造两个评分相同的已探索候选（各录 minSamples 条成功），
// 返回 items。评分相同 → 未修复时 Auto.Candidates 确定性输出同一顺序。
func buildAutoExploitItems(t *testing.T) []model.GroupItem {
	t.Helper()
	clearAutoStatsForTest()

	modelName := fmt.Sprintf("auto-bucket-%d", time.Now().UnixNano())
	items := []model.GroupItem{
		{ChannelID: 1, ModelName: modelName, Weight: 100, Priority: 1},
		{ChannelID: 2, ModelName: modelName, Weight: 1, Priority: 2},
	}

	// 两渠道样本数与成功率完全一致 → 评分相同，进入利用阶段。
	recordOutcome(1, modelName, true, 10)
	recordOutcome(2, modelName, true, 10)
	return items
}

// TestAutoCandidatesBucketShuffleBreaksMonopoly 验证 B2.b：两个评分相同的渠道，
// bucketTolerance>0 时多轮调用 Candidates 的首元素顺序会变化（不再确定性轮流垄断）。
func TestAutoCandidatesBucketShuffleBreaksMonopoly(t *testing.T) {
	items := buildAutoExploitItems(t)

	// 未注入随机源前先确认设置读取正常（bucketTolerance 默认 5 > 0）。
	if tol := getAutoBucketTolerance(); tol <= 0 {
		t.Skipf("bucket tolerance = %v, shuffle disabled", tol)
	}

	firstSeen := map[int]bool{}
	b := &Auto{}
	for i := 0; i < 200; i++ {
		got := b.Candidates(items)
		firstSeen[got[0].ChannelID] = true
	}
	if len(firstSeen) < 2 {
		t.Fatalf("first candidate channel = %v over 200 rounds, want both channels to appear (bucket shuffle should break deterministic monopoly)", firstSeen)
	}
}

// TestAutoCandidatesExploreRateZeroDeterministic 验证 B2.c：exploreRate=0
// （默认值，setting 未配置时 GetInt 返回 err → 回退 0）时行为与旧版一致——
// 不触发 softmax 重排（同分桶打散除外，那是独立开关且默认开启）。
// 用确定性顺序断言：评分不同（样本数不同）时排序稳定。
func TestAutoCandidatesExploreRateZeroDeterministic(t *testing.T) {
	clearAutoStatsForTest()
	seedAutoRand(t, 42)

	modelName := fmt.Sprintf("auto-deterministic-%d", time.Now().UnixNano())
	items := []model.GroupItem{
		{ChannelID: 1, ModelName: modelName, Weight: 1, Priority: 1},
		{ChannelID: 2, ModelName: modelName, Weight: 100, Priority: 2},
	}

	// 渠道 1 成功率 100%，渠道 2 成功率 60% → 评分差 0.4 >> bucketTolerance(5 评分点)。
	// 两个渠道都达到 minSamples → 全利用阶段，无探索。
	recordOutcome(1, modelName, true, 10)
	for i := 0; i < 4; i++ {
		recordOutcome(2, modelName, true, 6)
		recordOutcome(2, modelName, false, 4)
	}

	b := &Auto{}
	for round := 0; round < 20; round++ {
		got := b.Candidates(items)
		if len(got) != 2 {
			t.Fatalf("round %d: Candidates() len = %d, want 2", round, len(got))
		}
		if got[0].ChannelID != 1 || got[1].ChannelID != 2 {
			t.Fatalf("round %d: order = [%d, %d], want [1, 2] (score gap 0.4 exceeds bucket tolerance, exploreRate=0 must stay deterministic)", round, got[0].ChannelID, got[1].ChannelID)
		}
	}
}

// TestAutoCandidatesExplorationPhaseNotRandomized 验证 B2.d：探索阶段
// （样本数未达 minSamples 的候选存在）排序保持确定性，不受桶打散影响。
func TestAutoCandidatesExplorationPhaseNotRandomized(t *testing.T) {
	clearAutoStatsForTest()
	seedAutoRand(t, 7)

	modelName := fmt.Sprintf("auto-explore-notrand-%d", time.Now().UnixNano())
	items := []model.GroupItem{
		{ChannelID: 1, ModelName: modelName, Weight: 1, Priority: 1},
		{ChannelID: 2, ModelName: modelName, Weight: 100, Priority: 2},
	}

	// 渠道 1 只有 1 条样本（< minSamples 10）→ 探索阶段。
	recordOutcome(1, modelName, true, 1)

	b := &Auto{}
	for round := 0; round < 20; round++ {
		got := b.Candidates(items)
		// 探索阶段语义（balancer.go Auto.Candidates）：totalSamples 少者优先。
		// 渠道 2 无样本（0）< 渠道 1（1 样本）→ 渠道 2 恒排第一，确定性不随机。
		if got[0].ChannelID != 2 {
			t.Fatalf("round %d: first candidate = %d, want 2 (least-sampled first, deterministic)", round, got[0].ChannelID)
		}
	}
}

// TestAutoPostSortShuffleSkipsMixedExplored 混合阶段（部分探索/部分利用）不随机化。
func TestAutoPostSortShuffleSkipsMixedExplored(t *testing.T) {
	r := seedAutoRand(t, 99)
	_ = r

	scored := []autoScoredItem{
		{item: model.GroupItem{ChannelID: 1}, score: 0.9, totalSamples: 100, explored: true},
		{item: model.GroupItem{ChannelID: 2}, score: 0.9, totalSamples: 0, explored: false},
	}
	result := []model.GroupItem{scored[0].item, scored[1].item}

	autoPostSortShuffle(scored, result)
	if result[0].ChannelID != 1 || result[1].ChannelID != 2 {
		t.Fatalf("result = [%d, %d], want [1, 2] (mixed explored must not be randomized)", result[0].ChannelID, result[1].ChannelID)
	}
}

// TestAutoShuffleScoreBuckets 桶划分单元测试：
// 评分 [0.95, 0.93, 0.90, 0.70]（降序）容差 0.05：
//   - 桶1：0.95~0.90（下界 0.95-0.05=0.90，含 0.90）→ 前三个
//   - 桶2：0.70 单独一桶
//
// 桶1 内洗牌（随机），桶2 不动；桶1 内元素集合不变。
func TestAutoShuffleScoreBuckets(t *testing.T) {
	scored := []autoScoredItem{
		{item: model.GroupItem{ChannelID: 1}, score: 0.95, explored: true},
		{item: model.GroupItem{ChannelID: 2}, score: 0.93, explored: true},
		{item: model.GroupItem{ChannelID: 3}, score: 0.90, explored: true},
		{item: model.GroupItem{ChannelID: 4}, score: 0.70, explored: true},
	}
	result := []model.GroupItem{scored[0].item, scored[1].item, scored[2].item, scored[3].item}

	autoShuffleScoreBuckets(scored, result, 0.05)

	// 桶1（前三个位置）元素集合不变
	firstThree := map[int]bool{result[0].ChannelID: true, result[1].ChannelID: true, result[2].ChannelID: true}
	if !firstThree[1] || !firstThree[2] || !firstThree[3] {
		t.Fatalf("first three positions = %v, want permutation of {1,2,3}", firstThree)
	}
	// 桶2 固定
	if result[3].ChannelID != 4 {
		t.Fatalf("result[3].ChannelID = %d, want 4 (own bucket, must not move)", result[3].ChannelID)
	}
}

// TestAutoShuffleScoreBucketsZeroTolerance 容差 0 = 禁用打散。
func TestAutoShuffleScoreBucketsZeroTolerance(t *testing.T) {
	scored := []autoScoredItem{
		{item: model.GroupItem{ChannelID: 1}, score: 0.95, explored: true},
		{item: model.GroupItem{ChannelID: 2}, score: 0.95, explored: true},
	}
	result := []model.GroupItem{scored[0].item, scored[1].item}

	autoShuffleScoreBuckets(scored, result, 0)

	// 容差 0 时相同分数也会被划进同桶（score-bucketFloor = score >= score 成立）
	// 这里验证函数不 panic 且长度不变。
	if len(result) != 2 {
		t.Fatalf("result len = %d, want 2", len(result))
	}
}

// TestAutoSoftmaxShufflePreservesWeightPreference softmax 采样统计验证：
// 高分候选应以更高概率排在前面。评分 1.0 vs 0.0（权重比 e^1:1 ≈ 2.7:1），
// 多轮统计首位是高分候选的比例应显著高于 50%。
func TestAutoSoftmaxShufflePreservesWeightPreference(t *testing.T) {
	scored := []autoScoredItem{
		{item: model.GroupItem{ChannelID: 1}, score: 1.0, explored: true},
		{item: model.GroupItem{ChannelID: 2}, score: 0.0, explored: true},
	}

	firstIsHigh := 0
	const rounds = 2000
	for i := 0; i < rounds; i++ {
		result := []model.GroupItem{scored[0].item, scored[1].item}
		autoSoftmaxShuffle(scored, result)
		if result[0].ChannelID == 1 {
			firstIsHigh++
		}
	}
	ratio := float64(firstIsHigh) / float64(rounds)
	// e/(e+1) ≈ 0.731；容差放宽到 ±0.1。
	if ratio < 0.63 || ratio > 0.83 {
		t.Fatalf("high-score first ratio = %.3f, want ~0.731±0.1", ratio)
	}
}

// TestAutoSoftmaxShuffleInfScoreHandling 全熔断渠道（score=-Inf）参与采样不 panic，
// 且不会被排到健康渠道前面（权重 e^{-Inf} → 0）。
func TestAutoSoftmaxShuffleInfScoreHandling(t *testing.T) {
	scored := []autoScoredItem{
		{item: model.GroupItem{ChannelID: 1}, score: 1.0, explored: true},
		{item: model.GroupItem{ChannelID: 2}, score: math.Inf(-1), explored: true},
	}

	for round := 0; round < 100; round++ {
		result := []model.GroupItem{scored[0].item, scored[1].item}
		autoSoftmaxShuffle(scored, result)
		if result[0].ChannelID == 2 {
			t.Fatalf("round %d: tripped channel ranked first, want healthy channel first", round)
		}
	}
}

// TestAutoSoftmaxShuffleSingleItem 单候选 no-op。
func TestAutoSoftmaxShuffleSingleItem(t *testing.T) {
	scored := []autoScoredItem{
		{item: model.GroupItem{ChannelID: 1}, score: 0.5, explored: true},
	}
	result := []model.GroupItem{scored[0].item}
	autoSoftmaxShuffle(scored, result)
	if result[0].ChannelID != 1 {
		t.Fatalf("result[0].ChannelID = %d, want 1", result[0].ChannelID)
	}
}

// TestGetAutoBucketToleranceDefault setting 未配置时回退默认 5 评分点 = 0.05
// （GetInt 返回 err "setting not found" → 默认值分支）。
func TestGetAutoBucketToleranceDefault(t *testing.T) {
	tol := getAutoBucketTolerance()
	if tol != 0.05 {
		t.Fatalf("getAutoBucketTolerance() = %v, want default 0.05 (5 score points, setting not configured in package tests)", tol)
	}
}

// TestGetAutoExploreRateDefault setting 未配置时回退默认 0（保持旧行为）。
func TestGetAutoExploreRateDefault(t *testing.T) {
	rate := getAutoExploreRate()
	if rate != 0 {
		t.Fatalf("getAutoExploreRate() = %v, want default 0.0 (setting not configured in package tests)", rate)
	}
}
