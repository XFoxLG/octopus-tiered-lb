package balancer

import (
	"fmt"
	"math"
	"testing"
	"time"
)

// v2.9.0 软信号因子（softScoreFor）与渠道画像（决策原因/分析快照）的单测。
// 这些用例只依赖内存统计与 setting 默认值，不需要数据库。

func softWeightsForTest() softSignalWeights {
	return softSignalWeights{
		Enabled:         true,
		FakeStream:      0.15,
		EmptyOutput:     0.10,
		MissingTerminal: 0.10,
		Truncated:       0,
		Cap:             0.30,
		NeutralPrior:    0.50,
		DeadPenalty:     0.20,
	}
}

func uniqueSoftModel(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// TestSoftScoreNeutralPriorWhenSuccessSamplesInsufficient 验证冷启动先验：
// 成功样本数 < auto_strategy_min_samples 时即使已有软信号命中，也只返回中性先验，
// 不做按比例罚分（避免冷启动误判）。
func TestSoftScoreNeutralPriorWhenSuccessSamplesInsufficient(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	name := uniqueSoftModel("soft-neutral")
	stats := getOrCreateStats(51, name)
	for i := 0; i < 3; i++ {
		stats.recordSoftMask(softMaskFakeStream, time.Now())
	}

	w := softWeightsForTest()
	score, counts, streak := softScoreFor(stats, getTimeWindow(), getMinSamples()-1, w)
	if counts.FakeStream != 3 {
		t.Fatalf("FakeStream = %d, want 3", counts.FakeStream)
	}
	if streak != 0 {
		t.Fatalf("streak = %d, want 0", streak)
	}
	if math.Abs(score-w.NeutralPrior) > 1e-9 {
		t.Fatalf("score = %v, want neutral prior %v", score, w.NeutralPrior)
	}
}

// TestSoftScorePenaltyCapIsEnforced 验证罚分上限：把软信号窗口灌满假流式命中、
// 成功分母压到最小样本数，理论罚分应超过 cap，最终 softScore 必须等于 1-cap。
func TestSoftScorePenaltyCapIsEnforced(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	name := uniqueSoftModel("soft-cap")
	stats := getOrCreateStats(52, name)

	w := softWeightsForTest()
	hits := getSampleThreshold()
	for i := 0; i < hits; i++ {
		stats.recordSoftMask(softMaskFakeStream, time.Now())
	}
	denom := getMinSamples()
	rawPenalty := w.FakeStream * float64(hits) / float64(denom)
	if rawPenalty <= w.Cap {
		t.Fatalf("fixture does not exercise the cap: raw penalty %v <= cap %v (hits=%d denom=%d)",
			rawPenalty, w.Cap, hits, denom)
	}

	score, counts, _ := softScoreFor(stats, getTimeWindow(), denom, w)
	if counts.FakeStream != hits {
		t.Fatalf("FakeStream = %d, want %d", counts.FakeStream, hits)
	}
	want := 1 - w.Cap
	if math.Abs(score-want) > 1e-9 {
		t.Fatalf("score = %v, want %v (cap applied)", score, want)
	}
}

// TestSoftScoreDeadChannelExtraPenalty 验证「已判定死亡」的附加罚分：
// 尾部连续失败达到熔断阈值时，在正常罚分之上再扣 deadPenalty。
func TestSoftScoreDeadChannelExtraPenalty(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	name := uniqueSoftModel("soft-dead")
	deadStreak := int(getThreshold())
	recordOutcome(53, name, false, deadStreak)

	w := softWeightsForTest()
	score, counts, streak := softScoreFor(getOrCreateStats(53, name), getTimeWindow(), getMinSamples()*5, w)
	if counts.Total != 0 {
		t.Fatalf("soft counts total = %d, want 0 (no soft signals recorded)", counts.Total)
	}
	if int64(streak) < getThreshold() {
		t.Fatalf("streak = %d, want >= %d", streak, getThreshold())
	}
	want := 1 - w.DeadPenalty
	if math.Abs(score-want) > 1e-9 {
		t.Fatalf("score = %v, want %v", score, want)
	}
}

// TestRecordAutoDecisionReasonRoundTrip 验证决策原因写入/读回与非法输入保护。
func TestRecordAutoDecisionReasonRoundTrip(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	name := uniqueSoftModel("decision")
	reason := "capability: context_over_limit (est=65000 limit=32768 source=learned)"
	RecordAutoDecisionReason(61, name, "  "+reason+"  ")

	stats := getOrCreateStats(61, name)
	gotReason, gotAt := stats.lastDecision()
	if gotReason != reason {
		t.Fatalf("reason = %q, want %q", gotReason, reason)
	}
	if gotAt.IsZero() {
		t.Fatal("decision timestamp must be set")
	}

	// 非法输入（渠道非法/模型为空/原因为空）不得覆盖已有记录。
	RecordAutoDecisionReason(0, name, "ignored")
	RecordAutoDecisionReason(61, "   ", "ignored")
	RecordAutoDecisionReason(61, name, "   ")
	if r, _ := stats.lastDecision(); r != reason {
		t.Fatalf("reason was overwritten by invalid input: %q", r)
	}
}

// TestAutoStatsSnapshotCarriesSoftSignals 验证分析快照带出软信号率、上游模型
// 一致性与最近决策原因（分析中心「路由健康」的数据源）。
func TestAutoStatsSnapshotCarriesSoftSignals(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	name := uniqueSoftModel("snap-soft")
	RecordAutoSuccess(71, name)
	RecordAutoSuccess(71, name)
	RecordAutoSoftSignal(71, name, SoftSignalFakeStream)
	RecordAutoSoftSignal(71, name, SoftSignalMissingTerminal)
	RecordAutoSoftSignal(71, name, SoftSignalTruncated)
	RecordAutoUpstreamModel(71, name, true, true)   // 回报且不一致
	RecordAutoUpstreamModel(71, name, true, false)  // 回报且一致
	RecordAutoUpstreamModel(71, name, false, false) // 未回报 → 不计分母
	RecordAutoDecisionReason(71, name, "channel disabled")

	var got *AutoStatsSnapshotItem
	for _, item := range GetAutoStatsSnapshot([]int{71}) {
		if item.ModelName == name {
			copied := item
			got = &copied
		}
	}
	if got == nil {
		t.Fatalf("snapshot for %q not found", name)
	}
	if got.SampleCount != 2 || got.SoftSampleCount != 2 {
		t.Fatalf("samples = total %d / success %d, want 2/2", got.SampleCount, got.SoftSampleCount)
	}
	if got.FakeStreamCount != 1 || got.EmptyOutputCount != 0 || got.MissingTerminalCount != 1 || got.TruncatedCount != 1 {
		t.Fatalf("soft counts = fake:%d empty:%d missing:%d truncated:%d, want 1/0/1/1",
			got.FakeStreamCount, got.EmptyOutputCount, got.MissingTerminalCount, got.TruncatedCount)
	}
	if got.UpstreamModelSamples != 2 || got.UpstreamModelMismatch != 1 {
		t.Fatalf("upstream model samples = %d mismatch = %d, want 2/1",
			got.UpstreamModelSamples, got.UpstreamModelMismatch)
	}
	if math.Abs(got.SoftScore-softWeightsForTest().NeutralPrior) > 1e-9 {
		t.Fatalf("SoftScore = %v, want neutral prior (2 < minSamples)", got.SoftScore)
	}
	if got.ConsecutiveFailures != 0 || got.DeadChannel {
		t.Fatalf("dead channel fields = %d/%v, want 0/false", got.ConsecutiveFailures, got.DeadChannel)
	}
	if got.LastDecisionReason != "channel disabled" || got.LastDecisionAt.IsZero() {
		t.Fatalf("decision fields = %q/%v, want reason set and timestamp non-zero",
			got.LastDecisionReason, got.LastDecisionAt)
	}
}
