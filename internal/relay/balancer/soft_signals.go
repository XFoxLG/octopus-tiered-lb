package balancer

import (
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
)

// SoftSignalKind 是 v2.9.0 引入的「成功但可疑」软信号类型。
// 这些行为都会被现有链路记成成功（不计熔断/成功率），但会伤害真实体验，
// 因此作为独立维度进入 Auto 打分，而不是污染成功率。
type SoftSignalKind string

const (
	SoftSignalFakeStream      SoftSignalKind = "fake_stream"      // 首个可见内容时间占比 ≥60%（服务端复算，与前端假流式判据同源）
	SoftSignalEmptyOutput     SoftSignalKind = "empty_output"     // 成功但无可见输出（与 usage_state=empty_output 同源）
	SoftSignalMissingTerminal SoftSignalKind = "missing_terminal" // 上游流未发终止事件（usage_state=missing_terminal）
	SoftSignalTruncated       SoftSignalKind = "truncated"        // 触达长度上限（provider_termination_reason=length）
)

// softSignalMask 按位聚合信号，窗口条目只需 1 字节。
type softSignalMask uint8

const (
	softMaskFakeStream softSignalMask = 1 << iota
	softMaskEmptyOutput
	softMaskMissingTerminal
	softMaskTruncated
	// 上游自报模型一致性：Reported 计入分母，Mismatch 计入分子。
	softMaskModelReported
	softMaskModelMismatch
)

func softMaskForKind(kind SoftSignalKind) softSignalMask {
	switch kind {
	case SoftSignalFakeStream:
		return softMaskFakeStream
	case SoftSignalEmptyOutput:
		return softMaskEmptyOutput
	case SoftSignalMissingTerminal:
		return softMaskMissingTerminal
	case SoftSignalTruncated:
		return softMaskTruncated
	default:
		return 0
	}
}

// softSignalRecord 是一条软信号记录。软信号使用独立于请求窗口的环，避免
// 「挂到最近一条请求记录」在并发下错配（同一渠道的并发请求会交错写入）。
type softSignalRecord struct {
	Timestamp time.Time
	Mask      softSignalMask
}

// softSignalCounts 是时间窗口内的软信号计数。
type softSignalCounts struct {
	Total           int
	FakeStream      int
	EmptyOutput     int
	MissingTerminal int
	Truncated       int
	ModelReported   int
	ModelMismatches int
}

func (cs *ChannelStats) ensureSoftWindowLocked(size int) {
	if size <= 0 || len(cs.softWindow) == size {
		return
	}
	cs.softWindow = make([]softSignalRecord, size)
	cs.softHead = 0
	cs.softCount = 0
}

func (cs *ChannelStats) recordSoftMaskLocked(mask softSignalMask, now time.Time) {
	if mask == 0 {
		return
	}
	cs.ensureSoftWindowLocked(len(cs.window))
	cs.softWindow[cs.softHead] = softSignalRecord{Timestamp: now, Mask: mask}
	cs.softHead = (cs.softHead + 1) % len(cs.softWindow)
	if cs.softCount < len(cs.softWindow) {
		cs.softCount++
	}
}

func (cs *ChannelStats) recordSoftMask(mask softSignalMask, now time.Time) {
	if mask == 0 {
		return
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.recordSoftMaskLocked(mask, now)
}

// softCounts 统计时间窗口内的软信号命中次数。
func (cs *ChannelStats) softCounts(windowDuration time.Duration) softSignalCounts {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	var counts softSignalCounts
	if cs.softCount <= 0 {
		return counts
	}
	cutoff := time.Now().Add(-windowDuration)
	for i := 0; i < cs.softCount; i++ {
		rec := cs.softWindow[i]
		if !(rec.Timestamp.After(cutoff) || rec.Timestamp.Equal(cutoff)) {
			continue
		}
		counts.Total++
		if rec.Mask&softMaskFakeStream != 0 {
			counts.FakeStream++
		}
		if rec.Mask&softMaskEmptyOutput != 0 {
			counts.EmptyOutput++
		}
		if rec.Mask&softMaskMissingTerminal != 0 {
			counts.MissingTerminal++
		}
		if rec.Mask&softMaskTruncated != 0 {
			counts.Truncated++
		}
		if rec.Mask&softMaskModelReported != 0 {
			counts.ModelReported++
		}
		if rec.Mask&softMaskModelMismatch != 0 {
			counts.ModelMismatches++
		}
	}
	return counts
}

// consecutiveFailureTail 返回滑动窗口尾部（按时间从新到旧）连续失败的条数。
// 只统计时间窗口内的记录，避免长时间空闲后旧账迟迟不清。
func (cs *ChannelStats) consecutiveFailureTail(windowDuration time.Duration) int {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	if cs.count == 0 {
		return 0
	}
	cutoff := time.Now().Add(-windowDuration)
	streak := 0
	for i := 1; i <= cs.count; i++ {
		idx := (cs.head - i + len(cs.window)) % len(cs.window)
		rec := cs.window[idx]
		if !(rec.Timestamp.After(cutoff) || rec.Timestamp.Equal(cutoff)) {
			break
		}
		if rec.Success {
			break
		}
		streak++
	}
	return streak
}

// RecordAutoSoftSignal 记录一次成功响应上的软信号命中（假流式/空输出/流中断/截断）。
func RecordAutoSoftSignal(channelID int, modelName string, kind SoftSignalKind) {
	mask := softMaskForKind(kind)
	if mask == 0 {
		return
	}
	stats := getOrCreateStats(channelID, modelName)
	stats.recordSoftMask(mask, time.Now())
}

// RecordAutoUpstreamModel 记录一次成功响应的上游自报模型一致性样本。
// reported=false（上游未回报模型）时不计入分母，避免把「未回报」当成不一致。
func RecordAutoUpstreamModel(channelID int, modelName string, reported bool, mismatch bool) {
	if !reported {
		return
	}
	mask := softMaskModelReported
	if mismatch {
		mask |= softMaskModelMismatch
	}
	stats := getOrCreateStats(channelID, modelName)
	stats.recordSoftMask(mask, time.Now())
}

// setLastDecision 记录最近一次候选跳过原因。
func (cs *ChannelStats) setLastDecision(reason string, now time.Time) {
	cs.mu.Lock()
	cs.lastDecisionReason = reason
	cs.lastDecisionAt = now
	cs.mu.Unlock()
}

// lastDecision 读取最近一次候选跳过原因（无人记录时返回零值）。
func (cs *ChannelStats) lastDecision() (string, time.Time) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.lastDecisionReason, cs.lastDecisionAt
}

// RecordAutoDecisionReason 记录候选的最近一次跳过原因（v2.9.0 渠道画像）。
// 由 Iterator.Skip 统一调用，覆盖禁用/冷却/熔断/并发满/上下文超限等全部闸门。
func RecordAutoDecisionReason(channelID int, modelName, reason string) {
	reason = strings.TrimSpace(reason)
	modelName = strings.TrimSpace(modelName)
	if channelID <= 0 || modelName == "" || reason == "" {
		return
	}
	stats := getOrCreateStats(channelID, modelName)
	stats.setLastDecision(reason, time.Now())
}

// softSignalWeights 是归一化后的软信号配置（0-1 评分尺度）。
type softSignalWeights struct {
	Enabled         bool
	FakeStream      float64
	EmptyOutput     float64
	MissingTerminal float64
	Truncated       float64
	Cap             float64
	NeutralPrior    float64
	DeadPenalty     float64
}

func percentSetting(key model.SettingKey, fallback int) float64 {
	v, err := setting.GetInt(key)
	if err != nil || v < 0 || v > 100 {
		v = fallback
	}
	return float64(v) / 100.0
}

func getSoftSignalWeights() softSignalWeights {
	enabled := true
	if v, err := setting.GetBool(model.SettingKeyAutoSoftSignalsEnabled); err == nil {
		enabled = v
	}
	return softSignalWeights{
		Enabled:         enabled,
		FakeStream:      percentSetting(model.SettingKeyAutoSoftPenaltyFakeStream, 15),
		EmptyOutput:     percentSetting(model.SettingKeyAutoSoftPenaltyEmptyOutput, 10),
		MissingTerminal: percentSetting(model.SettingKeyAutoSoftPenaltyMissingTerminal, 10),
		Truncated:       percentSetting(model.SettingKeyAutoSoftPenaltyTruncated, 0),
		Cap:             percentSetting(model.SettingKeyAutoSoftPenaltyCap, 30),
		NeutralPrior:    percentSetting(model.SettingKeyAutoSoftNeutralPrior, 50),
		DeadPenalty:     percentSetting(model.SettingKeyAutoDeadChannelPenalty, 20),
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// softScoreFor 计算 (渠道,模型) 的软信号因子 softScore ∈ [0,1]。
//
// 语义（v2.9.0 计划）：
//   - 成功样本不足（< auto_strategy_min_samples）→ 中性先验（默认 0.5），避免冷启动误判；
//     Auto 的探索期先于本因子生效（未达最小样本的候选仍是探索优先级），因此冷启动
//     的整体行为与旧版本接近。
//   - 否则 softScore = 1 - Σ(权重 × 窗口命中率)，总罚分被 cap 截断（默认 0.30）。
//   - 尾部连续失败达到熔断阈值 = 已判定死亡的渠道，额外扣 deadPenalty（默认 0.20）。
func softScoreFor(stats *ChannelStats, windowDuration time.Duration, successSamples int, w softSignalWeights) (float64, softSignalCounts, int) {
	counts := stats.softCounts(windowDuration)
	streak := stats.consecutiveFailureTail(windowDuration)

	var score float64
	if successSamples < getMinSamples() {
		score = w.NeutralPrior
	} else {
		denom := float64(successSamples)
		penalty := w.FakeStream*float64(counts.FakeStream)/denom +
			w.EmptyOutput*float64(counts.EmptyOutput)/denom +
			w.MissingTerminal*float64(counts.MissingTerminal)/denom +
			w.Truncated*float64(counts.Truncated)/denom
		if penalty > w.Cap {
			penalty = w.Cap
		}
		score = 1 - penalty
	}
	if int64(streak) >= getThreshold() {
		score -= w.DeadPenalty
	}
	return clamp01(score), counts, streak
}
