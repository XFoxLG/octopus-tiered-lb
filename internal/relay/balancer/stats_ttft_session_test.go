package balancer

import (
	"fmt"
	"testing"
	"time"
)

// ---- B4: TTFT EMA 独立统计 ----

// TestRecordAutoTTFT_EMAConvergence 验证 TTFT EMA 的收敛行为：alpha=0.3 下
// 首个样本直接成为 EMA，后续样本按 e = 0.3*x + 0.7*e 平滑，且与总延迟 EMA 互不干扰。
func TestRecordAutoTTFT_EMAConvergence(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("ttft-ema-%d", time.Now().UnixNano())

	stats := getOrCreateStats(11, modelName)
	if got := stats.GetTTFT(); got != 0 {
		t.Fatalf("fresh stats GetTTFT() = %f, want 0", got)
	}

	RecordAutoTTFT(11, modelName, 100)
	stats = getOrCreateStats(11, modelName)
	if got := stats.GetTTFT(); got != 100 {
		t.Fatalf("after first sample GetTTFT() = %f, want 100 (first sample seeds EMA)", got)
	}

	RecordAutoTTFT(11, modelName, 200)
	// e = 0.3*200 + 0.7*100 = 130
	if got := stats.GetTTFT(); got < 129.99 || got > 130.01 {
		t.Fatalf("after second sample GetTTFT() = %f, want ~130", got)
	}

	RecordAutoTTFT(11, modelName, 200)
	// e = 0.3*200 + 0.7*130 = 151
	if got := stats.GetTTFT(); got < 150.99 || got > 151.01 {
		t.Fatalf("after third sample GetTTFT() = %f, want ~151", got)
	}

	// 连续同值样本应单调逼近该值
	for i := 0; i < 8; i++ {
		RecordAutoTTFT(11, modelName, 1000)
	}
	got := stats.GetTTFT()
	if got <= 151 {
		t.Fatalf("GetTTFT() = %f after 8x1000ms samples, should converge toward 1000 from 151", got)
	}
	if got >= 1000 {
		t.Fatalf("GetTTFT() = %f, EMA should approach but never overshoot 1000", got)
	}
}

// TestRecordAutoTTFT_IgnoresNonPositive 验证 ttftMs<=0 被忽略，不产生样本。
func TestRecordAutoTTFT_IgnoresNonPositive(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("ttft-zero-%d", time.Now().UnixNano())

	RecordAutoTTFT(12, modelName, 0)
	RecordAutoTTFT(12, modelName, -5)

	stats := getOrCreateStats(12, modelName)
	if got := stats.GetTTFT(); got != 0 {
		t.Fatalf("GetTTFT() = %f after 0/negative samples, want 0", got)
	}
	if stats.ttftSamples != 0 {
		t.Fatalf("ttftSamples = %d, want 0", stats.ttftSamples)
	}

	// 0 值不应污染后续 EMA：先记 0（忽略）再记 500，EMA 应为 500 而非被 0 拉低
	RecordAutoTTFT(12, modelName, 0)
	RecordAutoTTFT(12, modelName, 500)
	if got := stats.GetTTFT(); got != 500 {
		t.Fatalf("GetTTFT() = %f, want 500 (0 ignored, first valid sample seeds EMA)", got)
	}
}

// TestGetTTFT_NoSamplesReturnsZero 验证无样本渠道 GetTTFT 恒为 0，
// 且 TTFT 记录不影响总延迟 EMA、总延迟记录也不影响 TTFT EMA。
func TestGetTTFT_NoSamplesReturnsZero(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("ttft-indep-%d", time.Now().UnixNano())

	// 只记 TTFT：latency EMA 应保持 0
	RecordAutoTTFT(13, modelName, 300)
	stats := getOrCreateStats(13, modelName)
	if got := stats.GetLatency(); got != 0 {
		t.Fatalf("GetLatency() = %f after only TTFT samples, want 0 (independent EMAs)", got)
	}
	if got := stats.GetTTFT(); got != 300 {
		t.Fatalf("GetTTFT() = %f, want 300", got)
	}

	// 只记 latency 的渠道：TTFT 应保持 0
	modelName2 := fmt.Sprintf("ttft-indep2-%d", time.Now().UnixNano())
	RecordAutoLatency(14, modelName2, 800)
	stats2 := getOrCreateStats(14, modelName2)
	if got := stats2.GetTTFT(); got != 0 {
		t.Fatalf("GetTTFT() = %f after only latency samples, want 0 (independent EMAs)", got)
	}
	if got := stats2.GetLatency(); got != 800 {
		t.Fatalf("GetLatency() = %f, want 800", got)
	}
}

// ---- B5: 粘性会话评分逃生 ----

// TestShouldEvictSticky_BelowThresholdEvicts 验证样本足够且成功率低于阈值时触发逃生。
func TestShouldEvictSticky_BelowThresholdEvicts(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("sticky-evict-%d", time.Now().UnixNano())

	// getMinSamples() 默认 10（setting 未配置时回落 10）
	minSamples := getMinSamples()
	// 9 成 1 败不足以；凑足 minSamples 个样本且成功率 < 0.3
	recordOutcome(21, modelName, false, minSamples)
	recordOutcome(21, modelName, true, 1)

	if !ShouldEvictSticky(901, modelName, 21) {
		t.Fatalf("ShouldEvictSticky = false, want true (success rate 1/%d < 0.3 with enough samples)", minSamples+1)
	}
}

// TestShouldEvictSticky_InsufficientSamplesKeepsSticky 验证样本不足时不触发逃生。
func TestShouldEvictSticky_InsufficientSamplesKeepsSticky(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("sticky-few-%d", time.Now().UnixNano())

	// 全失败但只有 minSamples-1 个样本：样本不足，不逃生
	recordOutcome(22, modelName, false, getMinSamples()-1)

	if ShouldEvictSticky(902, modelName, 22) {
		t.Fatalf("ShouldEvictSticky = true with %d samples (< minSamples), want false", getMinSamples()-1)
	}
}

// TestShouldEvictSticky_HealthyChannelNotEvicted 验证健康渠道不触发逃生。
func TestShouldEvictSticky_HealthyChannelNotEvicted(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("sticky-ok-%d", time.Now().UnixNano())

	recordOutcome(23, modelName, true, 20)
	recordOutcome(23, modelName, false, 1)

	if ShouldEvictSticky(903, modelName, 23) {
		t.Fatalf("ShouldEvictSticky = true for healthy channel (20/21 success), want false")
	}
}

// TestGetSticky_EvictsOnCollapsedChannel 验证 GetSticky 在粘性渠道崩塌时
// 删除粘性记录并返回 nil；同时验证 RemoveSticky 直接删除生效。
func TestGetSticky_EvictsOnCollapsedChannel(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("sticky-get-%d", time.Now().UnixNano())

	SetSticky(904, modelName, 24, 2401)
	recordOutcome(24, modelName, false, getMinSamples())

	// 粘性渠道崩塌：GetSticky 应逃生返回 nil 并删除条目
	if entry := GetSticky(904, modelName, time.Hour); entry != nil {
		t.Fatalf("GetSticky = %+v on collapsed channel, want nil (evicted)", entry)
	}
	if entry := GetSticky(904, modelName, time.Hour); entry != nil {
		t.Fatalf("GetSticky after eviction = %+v, want nil (entry deleted, not just masked)", entry)
	}
}

// TestGetSticky_HealthyChannelReturnsEntry 验证健康粘性渠道不受逃生逻辑影响。
func TestGetSticky_HealthyChannelReturnsEntry(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("sticky-keep-%d", time.Now().UnixNano())

	SetSticky(905, modelName, 25, 2501)
	recordOutcome(25, modelName, true, getMinSamples())

	entry := GetSticky(905, modelName, time.Hour)
	if entry == nil {
		t.Fatalf("GetSticky = nil for healthy sticky channel, want entry")
	}
	if entry.ChannelID != 25 {
		t.Fatalf("GetSticky ChannelID = %d, want 25", entry.ChannelID)
	}
}

// TestRemoveSticky_DeletesEntry 验证 RemoveSticky 删除粘性条目。
func TestRemoveSticky_DeletesEntry(t *testing.T) {
	modelName := fmt.Sprintf("sticky-rm-%d", time.Now().UnixNano())

	SetSticky(906, modelName, 26, 2601)
	if entry := GetSticky(906, modelName, time.Hour); entry == nil {
		t.Fatalf("GetSticky = nil right after SetSticky, want entry")
	}

	RemoveSticky(906, modelName)
	if entry := GetSticky(906, modelName, time.Hour); entry != nil {
		t.Fatalf("GetSticky = %+v after RemoveSticky, want nil", entry)
	}

	// 幂等：删除不存在的条目不应 panic
	RemoveSticky(906, modelName)
}

// TestGetSticky_TTLExpiryUnchanged 验证逃生逻辑不破坏原有 TTL 过期行为。
func TestGetSticky_TTLExpiryUnchanged(t *testing.T) {
	clearAutoStatsForTest()
	t.Cleanup(clearAutoStatsForTest)

	modelName := fmt.Sprintf("sticky-ttl-%d", time.Now().UnixNano())

	// 无统计数据的渠道（样本 0 < minSamples，逃生不触发），仅靠 TTL 过期。
	// Windows 时钟粒度不足以让 time.Since 在写入后立刻 > 1ns，故白盒拨回时间戳
	// 保证 TTL 分支必然命中（逃生逻辑在此分支之前，两者互不干扰）。
	SetSticky(907, modelName, 27, 2701)
	if v, ok := globalSession.Load(sessionKey(907, modelName)); ok {
		if entry, ok := v.(*SessionEntry); ok {
			entry.Timestamp = time.Now().Add(-time.Hour)
		}
	}
	if entry := GetSticky(907, modelName, time.Nanosecond); entry != nil {
		t.Fatalf("GetSticky = %+v with expired timestamp, want nil (expired)", entry)
	}
}
