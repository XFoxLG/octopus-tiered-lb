package anthropic

import "math"

// Anthropic 手动 thinking（thinking.type=enabled）的官方硬约束：
//
//	budget_tokens ≥ 1024 且 budget_tokens < max_tokens
//
// 客户端经常给出违反约束的组合（budget 只写 512、budget 与 max_tokens 相等、
// max_tokens 小到装不下最小预算），原样转发会被上游 400 直接拒绝。这里按
// new-api relaykit 的既有口径在出站前钳制（`relaykit/relayconvert/reasoning/
// claude.go` 的 `claude_max_tokens_raised` / `claude_budget_adjusted` 两个诊断）：
//
//  1. max_tokens 缺失或 ≤1024（装不下最小预算）→ 抬高到 max(1280, budget+1)；
//  2. budget 下限 1024；若 budget ≥ max_tokens → 降到 max_tokens-1。
//
// 取舍：优先压低 budget 去适配客户端的 max_tokens（budget 调小不会顶破模型输出
// 上限，而抬高 max_tokens 有可能）；只有 max_tokens 本身装不下 1024 的最小预算时
// 才抬 max_tokens——与 new-api 的选择一致。
const (
	// minThinkingBudgetTokens 是 Anthropic 手动 thinking 的最小 budget_tokens。
	minThinkingBudgetTokens = int64(1024)
	// raisedMaxTokensFloor 是 max_tokens 被抬高时的保底值（1024 预算 + 余量），
	// 与 new-api relaykit 的取值一致。
	raisedMaxTokensFloor = int64(1280)
)

// clampThinkingBudget 返回钳制后的 (budgetTokens, maxTokens) 以及是否发生了调整。
// 入参为原始值，纯函数、无副作用，便于单测与跨出站复用。
func clampThinkingBudget(budgetTokens, maxTokens int64) (int64, int64, bool) {
	adjusted := false

	// 1) max_tokens 装不下最小预算：抬高 max_tokens（new-api: claude_max_tokens_raised）。
	if maxTokens <= minThinkingBudgetTokens {
		raised := raisedMaxTokensFloor
		if budgetTokens > 0 && budgetTokens < math.MaxInt64/2 && budgetTokens+1 > raised {
			raised = budgetTokens + 1
		}
		maxTokens = raised
		adjusted = true
	}

	// 2) budget 下限 1024 且必须严格小于 max_tokens（new-api: claude_budget_adjusted）。
	effectiveBudget := budgetTokens
	if effectiveBudget < minThinkingBudgetTokens {
		effectiveBudget = minThinkingBudgetTokens
	}
	if effectiveBudget >= maxTokens {
		effectiveBudget = maxTokens - 1
	}
	if effectiveBudget != budgetTokens {
		adjusted = true
	}

	return effectiveBudget, maxTokens, adjusted
}
