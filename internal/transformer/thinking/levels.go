// Package thinking holds the cross-protocol reasoning-effort ↔ token-budget
// table. Both the Anthropic and Gemini outbound transformers resolve reasoning
// effort through this package so that a single semantic tier maps to the same
// numeric budget everywhere.
//
// The table follows the de-facto ecosystem convention that CLIProxyAPI
// documents (internal/thinking/convert.go): minimal 512, low 1024, medium
// 8192, high 24576, xhigh 32768, max 128000. Protocol-specific transformers
// stay responsible for their own wire constraints (Gemini's per-family level
// clamping, Anthropic's budget < max_tokens rule).
package thinking

import "strings"

// Semantic effort tiers. These mirror the inbound Anthropic effort constants
// and the OpenAI reasoning_effort enum.
const (
	EffortNone    = "none"
	EffortMinimal = "minimal"
	EffortLow     = "low"
	EffortMedium  = "medium"
	EffortHigh    = "high"
	EffortXHigh   = "xhigh"
	EffortMax     = "max"
)

// Budget thresholds: the inclusive upper bound of each tier for the reverse
// mapping. Values above ThresholdHigh belong to xhigh. max has no threshold
// because nothing above xhigh can be inferred from a raw budget.
//
// Note None (0) and Auto (-1) are not budget *magnitudes* — callers that
// care about enable/disable semantics must special-case them before asking
// for a tier.
const (
	ThresholdMinimal = 512
	ThresholdLow     = 1024
	ThresholdMedium  = 8192
	ThresholdHigh    = 24576
)

const (
	// BudgetNone disables thinking where the provider supports it.
	BudgetNone = 0
	// BudgetAuto requests provider-side dynamic allocation.
	BudgetAuto = -1
	// BudgetXHigh is the canonical xhigh budget (no shorter threshold label
	// applies because max has no upper threshold).
	BudgetXHigh = 32768
	// BudgetMax is the canonical max budget.
	BudgetMax = 128000
	// BudgetDefault is the fallback used when an effort keyword is unknown and
	// the caller still has to emit a concrete budget (legacy behavior kept for
	// the Anthropic manual-thinking table).
	BudgetDefault = ThresholdMedium
)

// NormalizeEffort lowercases and trims an effort keyword. "off" is an alias
// for "none" because some clients use it interchangeably. Unknown values are
// returned unchanged so callers can decide whether to ignore them.
func NormalizeEffort(effort string) string {
	normalized := strings.ToLower(strings.TrimSpace(effort))
	if normalized == "off" {
		return EffortNone
	}
	return normalized
}

// EffortToBudget maps a semantic tier to its canonical budget. The second
// return value is false for unknown / empty efforts, in which case the caller
// should fall back to its own default rather than assuming a tier.
func EffortToBudget(effort string) (int64, bool) {
	switch NormalizeEffort(effort) {
	case EffortNone:
		return BudgetNone, true
	case EffortMinimal:
		return ThresholdMinimal, true
	case EffortLow:
		return ThresholdLow, true
	case EffortMedium:
		return ThresholdMedium, true
	case EffortHigh:
		return ThresholdHigh, true
	case EffortXHigh:
		return BudgetXHigh, true
	case EffortMax:
		return BudgetMax, true
	default:
		return 0, false
	}
}

// BudgetToEffort returns the semantic tier that a positive budget falls into.
// Tier 0 and below are control values, not magnitudes, so they report false;
// callers that need to distinguish disable/dynamic should compare against
// BudgetNone / BudgetAuto directly.
func BudgetToEffort(budget int64) (string, bool) {
	switch {
	case budget <= 0:
		return "", false
	case budget <= ThresholdMinimal:
		return EffortMinimal, true
	case budget <= ThresholdLow:
		return EffortLow, true
	case budget <= ThresholdMedium:
		return EffortMedium, true
	case budget <= ThresholdHigh:
		return EffortHigh, true
	default:
		return EffortXHigh, true
	}
}
