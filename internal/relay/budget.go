package relay

// forwardedCounter 暴露当前路由迭代器已发生的真实转发数。balancer.Iterator 实现此接口；
// 测试中可用轻量替身避免构造完整迭代器与渠道依赖。
type forwardedCounter interface {
	ForwardedAttempts() int
}

// relayBudget 跟踪跨路由轮次的「真实转发预算」与「零进展」状态，把 issue #95 与 #192
// 试图用单一计数同时解决的两件事拆开，修复 #95 -> #192 回归：
//
//	预算1 真实转发（forwardedBase）：累计真实发往上游的 HTTP 请求数，是 maxTotalAttempts
//	  的唯一计数源。冷却/熔断跳过不消耗它——否则前 N 个候选全在冷却时，第 N+1 个健康渠道
//	  永远不可达（#192 把跳过也计入 64 上限正是这个回归）。
//	预算2 零进展快速失败：上一轮完整跑完但零真实转发，即判定所有渠道当前不可用，直接 502。
//	  这取代 #192「跳过占预算」的兜底，且不会饿死健康渠道。
//	预算3 日志明细：跳过/熔断明细上限由 balancer.Iterator.maxSkipAttemptRecords 约束，
//	  与本预算解耦，不影响选路。
//
// 每轮使用顺序（顺序很重要，见各方法注释）：
//  1. shouldFastFail(routeRound) 用上一轮状态判定是否快速失败
//  2. markRoundStart() 重置 roundAppended
//  3. 内层循环中 reachedMaxForwarded(routeIter) 判断真实转发是否达上限
//  4. markRoundEnd(routeIter) 累加 forwardedBase
//
// shouldFastFail 必须在 markRoundStart 之前调用：roundAppended 此时仍保留上一轮完成状态。
// 原实现「先重置 roundAppended=false 再检查」使零进展条件恒为 false，是死代码。
type relayBudget struct {
	maxTotalAttempts   int
	forwardedBase      int
	lastRoundForwarded int
	roundAppended      bool
}

func newRelayBudget(maxTotalAttempts int) *relayBudget {
	return &relayBudget{maxTotalAttempts: maxTotalAttempts}
}

// reachedMaxForwarded 判断累计真实转发是否已达上限。当前迭代器本轮已发生的转发通过
// iter.ForwardedAttempts() 累加；跳过/熔断不计入，因此健康渠道不会被「跳过墙」挡住。
// maxTotalAttempts <= 0 表示未设上限（调用方已回退为默认值，此处防御性兜底）。
func (b *relayBudget) reachedMaxForwarded(iter forwardedCounter) bool {
	if b.maxTotalAttempts <= 0 {
		return false
	}
	return (b.forwardedBase + iter.ForwardedAttempts()) >= b.maxTotalAttempts
}

// shouldFastFail 判断是否应因上一轮零进展而快速失败。三个条件同时成立：
//   - routeRound > 1：已有上一轮可评判；
//   - roundAppended：上一轮完整跑完（未被中途 exhausted 打断）；
//   - lastRoundForwarded == 0：上一轮没有任何真实转发（全部冷却/熔断跳过）。
//
// 必须在 markRoundStart 之前调用，否则 roundAppended 被重置为 false 后判断恒为否。
func (b *relayBudget) shouldFastFail(routeRound int) bool {
	return routeRound > 1 && b.roundAppended && b.lastRoundForwarded == 0
}

// markRoundStart 重置本轮 roundAppended 为 false。必须在 shouldFastFail 之后调用，
// 以便零进展判断仍能看到上一轮的完成状态。
func (b *relayBudget) markRoundStart() {
	b.roundAppended = false
}

// markRoundEnd 累加本轮真实转发到 forwardedBase，记录本轮转发数，并标记本轮完成。
// 正常轮次结束后调用；轮次中途 exhausted 不调用，roundCompleted() 保持 false。
func (b *relayBudget) markRoundEnd(iter forwardedCounter) {
	forwarded := iter.ForwardedAttempts()
	b.forwardedBase += forwarded
	b.lastRoundForwarded = forwarded
	b.roundAppended = true
}

// roundCompleted 暴露给 exhausted 处理逻辑：轮次中途 exhausted 时为 false，需补齐
// 当前迭代器的决策记录到 allAttempts；轮次正常结束时为 true，不重复追加。
func (b *relayBudget) roundCompleted() bool {
	return b.roundAppended
}
