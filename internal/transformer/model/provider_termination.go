package model

import "strings"

// Provider terminal vocabulary is shared by forwarding and capability probes.
func TerminationFromAnthropicStopReason(stopReason, stopSequence *string) TerminationMetadata {
	termination := TerminationMetadata{}
	if stopReason == nil {
		return termination
	}
	termination.ProviderReason = *stopReason
	if stopSequence != nil {
		termination.StopSequence = *stopSequence
	}
	switch strings.ToLower(strings.TrimSpace(*stopReason)) {
	case "end_turn":
		termination.Cause = TerminationCauseComplete
	case "max_tokens":
		termination.Cause = TerminationCauseTokenLimit
	case "stop_sequence":
		termination.Cause = TerminationCauseStopSequence
	case "tool_use":
		termination.Cause = TerminationCauseToolCall
	case "pause_turn":
		termination.Cause = TerminationCausePauseTurn
	case "model_context_window_exceeded":
		termination.Cause = TerminationCauseContextExhausted
	case "refusal":
		termination.Cause = TerminationCauseRefusal
	default:
		termination.Cause = TerminationCauseUnknown
	}
	return termination
}

// Unspecified Gemini finishReason is field presence without terminal evidence.
func TerminationFromGeminiFinishReason(providerReason string) (TerminationMetadata, bool) {
	termination := TerminationMetadata{ProviderReason: providerReason}
	switch strings.ToUpper(strings.TrimSpace(providerReason)) {
	case "", "FINISH_REASON_UNSPECIFIED":
		return termination, false
	case "STOP":
		termination.Cause = TerminationCauseComplete
	case "MAX_TOKENS":
		termination.Cause = TerminationCauseTokenLimit
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "MODEL_ARMOR":
		termination.Cause = TerminationCauseContentFilter
	case "RECITATION", "IMAGE_RECITATION":
		termination.Cause = TerminationCauseRecitation
	case "LANGUAGE":
		termination.Cause = TerminationCauseUnknown
	case "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL":
		termination.Cause = TerminationCauseMalformedToolCall
	case "NO_IMAGE":
		termination.Cause = TerminationCauseError
	default:
		termination.Cause = TerminationCauseUnknown
	}
	return termination, true
}

func TerminationFromResponsesStatus(status *string, incompleteReason, errorMessage string) TerminationMetadata {
	if status == nil {
		return TerminationMetadata{}
	}
	termination := TerminationMetadata{ProviderReason: *status}
	switch strings.ToLower(strings.TrimSpace(*status)) {
	case "completed":
		termination.Cause = TerminationCauseComplete
	case "incomplete":
		termination.Cause, termination.Detail = TerminationCauseTokenLimit, incompleteReason
		switch strings.ToLower(strings.TrimSpace(incompleteReason)) {
		case "content_filter", "safety":
			termination.Cause = TerminationCauseContentFilter
		case "context_length_exceeded", "model_context_window_exceeded":
			termination.Cause = TerminationCauseContextExhausted
		}
	case "failed":
		termination.Cause, termination.Detail = TerminationCauseError, errorMessage
	case "cancelled", "canceled":
		termination.Cause = TerminationCauseTransportInterrupted
	case "queued", "in_progress":
		return TerminationMetadata{}
	default:
		termination.Cause = TerminationCauseUnknown
	}
	return termination
}

// Failure precedence is stable across choice order and repeated finish frames.
func MergeTermination(current, next TerminationMetadata) TerminationMetadata {
	if !next.HasCause() {
		return current
	}
	if !current.HasCause() || terminalPriority(next.Cause) > terminalPriority(current.Cause) {
		return next
	}
	return current
}

func terminalPriority(cause TerminationCause) int {
	if cause.IsProviderRefusal() {
		return 5
	}
	if cause.IsProviderFailure() {
		return 4
	}
	if cause == TerminationCauseContextExhausted || cause == TerminationCausePauseTurn {
		return 3
	}
	if cause == TerminationCauseTokenLimit {
		return 2
	}
	return 1
}
