package relay

import "testing"

// fakeForwardedCounter 是 forwardedCounter 的测试替身，模拟某轮迭代器已发生的真实转发数。
// 真实 balancer.Iterator 的 ForwardedAttempts 会随 StartAttempt/Skip 动态变化；测试中直接注入。
type fakeForwardedCounter struct {
	forwarded int
}

func (f fakeForwardedCounter) ForwardedAttempts() int { return f.forwarded }

// TestRelayBudget_SkipsDoNotConsumeBudget 验证 #95 -> #192 回归的核心修复：
// 前 64 个候选全在冷却/熔断（被 Skip，ForwardedAttempts=0）时，真实转发预算不应被消耗，
// 第 65 个健康渠道仍可达。原 #192 实现把跳过也计入 64 上限，健康渠道被「跳过墙」挡死。
func TestRelayBudget_SkipsDoNotConsumeBudget(t *testing.T) {
	budget := newRelayBudget(64)

	// 64 个候选全被跳过：本轮 ForwardedAttempts 仍为 0，跨轮 forwardedBase 也为 0。
	sixtyFourSkips := fakeForwardedCounter{forwarded: 0}
	if budget.reachedMaxForwarded(sixtyFourSkips) {
		t.Fatalf("64 skips should not exhaust forwarded budget; healthy 65th candidate must stay reachable")
	}

	// 第 65 个健康候选发起 1 次真实转发后，累计仅 1，仍未达 64 上限，应继续可尝试。
	oneForward := fakeForwardedCounter{forwarded: 1}
	if budget.reachedMaxForwarded(oneForward) {
		t.Fatalf("1 real forward should not exhaust a budget of 64")
	}
}

// TestRelayBudget_ForwardedBudgetAccumulatesAcrossRounds 验证 forwardedBase 跨轮累加，
// 且只统计真实转发。前一轮 5 次真实转发 + 本轮 3 次 = 8，达到上限 8 才停止。
func TestRelayBudget_ForwardedBudgetAccumulatesAcrossRounds(t *testing.T) {
	budget := newRelayBudget(8)

	// 第 1 轮：5 次真实转发
	budget.markRoundEnd(fakeForwardedCounter{forwarded: 5})
	if budget.reachedMaxForwarded(fakeForwardedCounter{forwarded: 0}) {
		t.Fatalf("forwardedBase=5 should not yet reach budget of 8")
	}
	if budget.forwardedBase != 5 {
		t.Fatalf("forwardedBase = %d, want 5 after round 1", budget.forwardedBase)
	}

	// 第 2 轮开始：先快速失败判断（上一轮有进展，不应触发），再 markRoundStart
	if budget.shouldFastFail(2) {
		t.Fatalf("shouldFastFail must be false when previous round had real forwards")
	}
	budget.markRoundStart()
	// 第 2 轮进行 3 次真实转发：5 + 3 = 8 达到上限
	if !budget.reachedMaxForwarded(fakeForwardedCounter{forwarded: 3}) {
		t.Fatalf("forwardedBase=5 + 3 this round = 8 should reach budget of 8")
	}
}

// TestRelayBudget_FastFailOnZeroProgress 验证零进展快速失败（预算2）真正生效。
// 这是原实现的死代码 bug：roundAppended 先被重置为 false 再检查，条件恒为 false。
// 现在 shouldFastFail 在 markRoundStart 之前调用，能看到上一轮完成状态并正确触发。
func TestRelayBudget_FastFailOnZeroProgress(t *testing.T) {
	budget := newRelayBudget(64)

	// 第 1 轮：所有候选都被冷却/熔断跳过，零真实转发
	budget.markRoundEnd(fakeForwardedCounter{forwarded: 0})

	// 第 2 轮开始：上一轮零进展，应快速失败
	if !budget.shouldFastFail(2) {
		t.Fatalf("shouldFastFail must be true when previous round had zero real forwards (all channels unavailable)")
	}
}

// TestRelayBudget_NoFastFailOnFirstRound 验证第 1 轮不会误判快速失败（无上一轮可评判）。
func TestRelayBudget_NoFastFailOnFirstRound(t *testing.T) {
	budget := newRelayBudget(64)
	if budget.shouldFastFail(1) {
		t.Fatalf("shouldFastFail must be false on round 1 (no previous round to judge)")
	}
}

// TestRelayBudget_NoFastFailAfterMidRoundExhaust 验证轮次中途 exhausted（未调 markRoundEnd）
// 不会误判为「上一轮完整跑完」，避免下一轮错误地快速失败。
func TestRelayBudget_NoFastFailAfterMidRoundExhaust(t *testing.T) {
	budget := newRelayBudget(64)
	// 模拟第 1 轮中途 exhausted：只 markRoundStart，不 markRoundEnd
	budget.markRoundStart()
	// 第 2 轮：roundAppended 仍为 false（上一轮未完整结束），不应快速失败
	if budget.shouldFastFail(2) {
		t.Fatalf("shouldFastFail must be false when previous round was interrupted (roundAppended=false)")
	}
}

// TestRelayBudget_DisabledWhenMaxZero 验证 maxTotalAttempts<=0 时预算检查关闭。
func TestRelayBudget_DisabledWhenMaxZero(t *testing.T) {
	budget := newRelayBudget(0)
	if budget.reachedMaxForwarded(fakeForwardedCounter{forwarded: 1000}) {
		t.Fatalf("reachedMaxForwarded must be false when maxTotalAttempts<=0 (budget disabled)")
	}
	budgetNegative := newRelayBudget(-1)
	if budgetNegative.reachedMaxForwarded(fakeForwardedCounter{forwarded: 1000}) {
		t.Fatalf("reachedMaxForwarded must be false when maxTotalAttempts<0 (budget disabled)")
	}
}

// TestRelayBudget_RoundCompletedTracking 验证 roundCompleted 供 exhausted 处理逻辑
// 判断是否需要补齐当前迭代器的决策记录：正常结束为 true，中途 exhausted 为 false。
func TestRelayBudget_RoundCompletedTracking(t *testing.T) {
	budget := newRelayBudget(64)
	// 初始 / 轮次开始：未完成
	budget.markRoundStart()
	if budget.roundCompleted() {
		t.Fatalf("roundCompleted must be false at round start (mid-round exhausted needs append)")
	}
	// 轮次正常结束：完成
	budget.markRoundEnd(fakeForwardedCounter{forwarded: 2})
	if !budget.roundCompleted() {
		t.Fatalf("roundCompleted must be true after markRoundEnd")
	}
	// 下一轮开始：又变为未完成
	budget.markRoundStart()
	if budget.roundCompleted() {
		t.Fatalf("roundCompleted must be false again after next markRoundStart")
	}
}

// TestRelayBudget_BoundaryExactMax 验证刚好达到上限时停止（边界 off-by-one）。
// forwardedBase=63 + 本轮 1 = 64 >= 64 → 达到上限；forwardedBase=63 + 本轮 0 → 未达。
func TestRelayBudget_BoundaryExactMax(t *testing.T) {
	budget := newRelayBudget(64)
	budget.markRoundEnd(fakeForwardedCounter{forwarded: 63})
	budget.markRoundStart()
	if budget.reachedMaxForwarded(fakeForwardedCounter{forwarded: 0}) {
		t.Fatalf("forwardedBase=63 + 0 = 63 should not reach budget of 64")
	}
	if !budget.reachedMaxForwarded(fakeForwardedCounter{forwarded: 1}) {
		t.Fatalf("forwardedBase=63 + 1 = 64 should reach budget of 64")
	}
}
