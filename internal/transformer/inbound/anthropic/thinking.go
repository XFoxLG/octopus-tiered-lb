package anthropic

import crossThinking "github.com/lingyuins/octopus/internal/transformer/thinking"

// thinkingBudgetToReasoningEffort converts thinking budget tokens to the
// cross-protocol reasoning effort string. Thresholds live in
// internal/transformer/thinking so every outbound protocol shares one table.
//
// Historical note: this used to cut off at 5000/15000 and could never emit
// xhigh. Budgets that used to read as "high" (>15000) now split into high
// (≤24576) and xhigh (>24576); the raw budget itself is preserved on the
// request, so downstream adapters that act on the explicit budget rather than
// the keyword are unaffected.
func thinkingBudgetToReasoningEffort(budgetTokens int64) string {
	if effort, ok := crossThinking.BudgetToEffort(budgetTokens); ok {
		// Claude adaptive effort accepts max/xhigh natively, so a huge budget
		// maps straight through instead of being flattened to high.
		if effort == crossThinking.EffortXHigh && budgetTokens >= crossThinking.BudgetMax {
			return EffortMax
		}
		return effort
	}
	// 0 and negative budgets are control values (disable / dynamic), not
	// magnitudes; keep the historical floor so callers can tell that the
	// client expressed an intent.
	return EffortLow
}
