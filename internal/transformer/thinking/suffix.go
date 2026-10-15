package thinking

import (
	"strconv"
	"strings"
)

// SuffixConfig describes a parsed model-name thinking suffix.
//
// The on-the-wire grammar is `model(value)` and follows the ecosystem
// convention that CLIProxyAPI documents (internal/thinking/suffix.go):
//
//	model(8192)     → ModelName=model, Budget=8192
//	model(high)     → ModelName=model, Effort="high"
//	model(none)     → ModelName=model, Budget=0 (disable thinking)
//	model(auto)     → ModelName=model, Budget=-1 (provider-side dynamic)
//	model(-1)       → ModelName=model, Budget=-1 (numeric spelling of auto)
//	model(foo)      → HasSuffix=false, ModelName unchanged
//
// Only the *last* parenthesized group is considered, and only when it is a
// recognised thinking value. Model names that legitimately contain other
// parentheses (`foo(experimental)`) are left untouched so routing keeps
// matching them.
type SuffixConfig struct {
	// ModelName is the upstream model name with any recognised suffix removed.
	ModelName string
	// HasSuffix reports whether a recognised thinking suffix was present.
	HasSuffix bool
	// Effort is the semantic tier for level suffixes (minimal/low/medium/
	// high/xhigh/max). Empty for numeric and special suffixes.
	Effort string
	// Budget carries the numeric budget for numeric/special suffixes
	// (0 = disable, -1 = dynamic). Its value is only meaningful when
	// HasSuffix is true and Effort is empty.
	Budget int64
}

// ParseModelSuffix splits a model name into its base name and thinking
// override. When the suffix is absent or not recognised the original model
// name is returned unchanged with HasSuffix=false.
func ParseModelSuffix(model string) SuffixConfig {
	lastOpen := strings.LastIndex(model, "(")
	if lastOpen < 0 || len(model) == 0 || model[len(model)-1] != ')' {
		return SuffixConfig{ModelName: model}
	}
	base := model[:lastOpen]
	raw := model[lastOpen+1 : len(model)-1]
	if base == "" || raw == "" {
		return SuffixConfig{ModelName: model}
	}

	if effort, ok := ParseLevelSuffix(raw); ok {
		return SuffixConfig{ModelName: base, HasSuffix: true, Effort: effort}
	}
	if budget, ok := ParseSpecialSuffix(raw); ok {
		return SuffixConfig{ModelName: base, HasSuffix: true, Budget: budget}
	}
	if budget, ok := ParseNumericSuffix(raw); ok {
		return SuffixConfig{ModelName: base, HasSuffix: true, Budget: budget}
	}
	return SuffixConfig{ModelName: model}
}

// ParseLevelSuffix recognises the discrete effort tiers. Matching is
// case-insensitive. Special values (none/auto/-1) are handled by
// ParseSpecialSuffix instead.
func ParseLevelSuffix(raw string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax:
		return normalized, true
	}
	return "", false
}

// ParseSpecialSuffix recognises the non-numeric control values: "none"
// disables thinking (budget 0) and both "auto" and "-1" request dynamic
// allocation (budget -1). Matching is case-insensitive.
func ParseSpecialSuffix(raw string) (int64, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case EffortNone:
		return BudgetNone, true
	case "auto", "-1":
		return BudgetAuto, true
	}
	return 0, false
}

// ParseNumericSuffix parses a non-negative integer budget. Leading zeros are
// accepted; negatives and overflows are rejected so they fall back to the
// original model name.
func ParseNumericSuffix(raw string) (int64, bool) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}
