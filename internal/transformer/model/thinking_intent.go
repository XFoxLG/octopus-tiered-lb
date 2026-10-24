package model

import (
	"errors"
	"fmt"
	"strings"
)

// ErrReasoningControlsConflict marks a client request that asks for thinking on
// and off at the same precedence. Callers should surface it as 400 before any
// upstream attempt is made.
var ErrReasoningControlsConflict = errors.New("conflicting reasoning controls")

// ReasoningIntent is the normalised reasoning intent expressed by one source
// (client body, model-name suffix override, or group default) after the inbound
// adapter has mapped it onto the internal request fields.
type ReasoningIntent uint8

const (
	// ReasoningIntentUnspecified means the source expressed no reasoning intent
	// at all, so a lower-precedence default may fill it in.
	ReasoningIntentUnspecified ReasoningIntent = iota
	// ReasoningIntentOn means the source explicitly asked for thinking without a
	// level or budget, e.g. enable_thinking=true.
	ReasoningIntentOn
	// ReasoningIntentOff means the source explicitly turned thinking off. That
	// covers enable_thinking=false, reasoning_effort="none", an Anthropic
	// thinking.disabled block, and an empty Responses reasoning object (all of
	// which arrive as ReasoningExplicit with no positive level).
	ReasoningIntentOff
	// ReasoningIntentLevel means the source gave a concrete level, an explicit
	// budget, or an adaptive request.
	ReasoningIntentLevel
)

// ReasoningHasLevel reports whether the request carries a concrete level,
// explicit budget, or adaptive request, as opposed to a bare on/off flag.
func ReasoningHasLevel(request *InternalLLMRequest) bool {
	if request == nil {
		return false
	}
	if request.ReasoningBudget != nil || request.AdaptiveThinking {
		return true
	}
	return isLevelEffort(request.ReasoningEffort)
}

// ReasoningIntentOf classifies the intent carried by the internal request
// fields, regardless of which inbound protocol produced them.
//
// It is deliberately tolerant of internally derived combinations: the internal
// request is the union of every inbound mapping, and several mappings set more
// than one field for a single client choice (Anthropic thinking-enabled sets
// both effort and budget; adaptive sets effort plus the adaptive flag; the
// model-name suffix sets ReasoningExplicit plus effort/budget). Those are valid,
// so a concrete level always wins the classification over a bare enable flag.
func ReasoningIntentOf(request *InternalLLMRequest) ReasoningIntent {
	if request == nil {
		return ReasoningIntentUnspecified
	}
	if ReasoningHasLevel(request) {
		return ReasoningIntentLevel
	}
	if request.EnableThinking != nil {
		if *request.EnableThinking {
			return ReasoningIntentOn
		}
		return ReasoningIntentOff
	}
	if strings.EqualFold(strings.TrimSpace(request.ReasoningEffort), "none") || request.ReasoningExplicit {
		return ReasoningIntentOff
	}
	return ReasoningIntentUnspecified
}

// ValidateClientReasoningControls rejects a client request that carries two
// contradictory reasoning controls at the same precedence and returns an error
// wrapping ErrReasoningControlsConflict.
//
// Call it on the request as parsed from the client body and BEFORE the
// model-name suffix override is applied. The suffix is a higher-precedence
// client choice that silently wins, while two controls inside the same body are
// a hard error the gateway answers with 400 before any upstream call.
//
// Only genuinely contradictory client controls are rejected. Combinations that
// a single client choice is mapped into by the inbound adapters (Anthropic
// thinking-enabled producing both effort and budget, adaptive producing effort
// plus the adaptive flag, Responses reasoning{} producing ReasoningExplicit with
// no level) are valid and pass through untouched.
func ValidateClientReasoningControls(request *InternalLLMRequest) error {
	if request == nil {
		return nil
	}

	level := ReasoningHasLevel(request)
	explicitOff := (request.EnableThinking != nil && !*request.EnableThinking) ||
		strings.EqualFold(strings.TrimSpace(request.ReasoningEffort), "none")
	explicitOn := request.EnableThinking != nil && *request.EnableThinking

	switch {
	case explicitOff && level:
		return fmt.Errorf("%w: enable_thinking=false cannot be combined with a reasoning effort, budget or adaptive thinking", ErrReasoningControlsConflict)
	case explicitOn && strings.EqualFold(strings.TrimSpace(request.ReasoningEffort), "none"):
		return fmt.Errorf("%w: enable_thinking=true cannot be combined with reasoning_effort=none", ErrReasoningControlsConflict)
	}
	return nil
}

func isLevelEffort(effort string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(effort))
	return trimmed != "" && trimmed != "none"
}
