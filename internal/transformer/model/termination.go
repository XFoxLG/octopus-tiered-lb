package model

// TerminationCause is the protocol-agnostic classification of why a model
// response stopped. Outbound adapters decode provider-specific reasons into
// this canonical form so that relay policy (retry, health, logging) never has
// to understand strings like "MAX_TOKENS" or "pause_turn" directly.
//
// This value never reaches the client: inbound adapters render it back into the
// wire vocabulary of whichever protocol the client speaks.
type TerminationCause string

const (
	// TerminationCauseUnspecified means no termination information was decoded.
	TerminationCauseUnspecified TerminationCause = ""

	// TerminationCauseComplete is a natural, complete stop.
	TerminationCauseComplete TerminationCause = "complete"

	// TerminationCauseStopSequence means generation halted on a caller-supplied
	// stop sequence. Semantically complete, but distinct from a natural stop.
	TerminationCauseStopSequence TerminationCause = "stop_sequence"

	// TerminationCauseToolCall means the model yielded to wait for tool results.
	TerminationCauseToolCall TerminationCause = "tool_call"

	// TerminationCausePauseTurn means the provider paused a long-running server
	// side turn and expects the caller to resume it by replaying the assistant
	// content. This is NOT a completed turn and must not be retried from
	// scratch.
	TerminationCausePauseTurn TerminationCause = "pause_turn"

	// TerminationCauseTokenLimit means output was cut off by a token budget.
	TerminationCauseTokenLimit TerminationCause = "token_limit"

	// TerminationCauseContextExhausted means the prompt itself filled the
	// context window. Replaying the identical request cannot succeed.
	TerminationCauseContextExhausted TerminationCause = "context_exhausted"

	// TerminationCauseContentFilter means output was withheld by a safety or
	// policy filter on the provider side.
	TerminationCauseContentFilter TerminationCause = "content_filter"

	// TerminationCauseRecitation means output was withheld for reciting
	// protected material. Kept separate from content_filter because the two
	// have different operational meaning.
	TerminationCauseRecitation TerminationCause = "recitation"

	// TerminationCausePromptBlocked means the request never produced a
	// candidate because the prompt itself was rejected.
	TerminationCausePromptBlocked TerminationCause = "prompt_blocked"

	// TerminationCauseRefusal means the model explicitly declined.
	TerminationCauseRefusal TerminationCause = "refusal"

	// TerminationCauseMalformedToolCall means the provider reported that it
	// failed to emit a well-formed tool call.
	TerminationCauseMalformedToolCall TerminationCause = "malformed_tool_call"

	// TerminationCauseError means the provider reported an explicit failure.
	TerminationCauseError TerminationCause = "error"

	// TerminationCauseMissingTerminal means the transport closed cleanly but no
	// protocol-level terminal marker was ever observed. The response may be
	// silently incomplete even though HTTP reported success.
	TerminationCauseMissingTerminal TerminationCause = "missing_terminal"

	// TerminationCauseTransportInterrupted means the stream broke after
	// producing output: read error, timeout, or idle stall.
	TerminationCauseTransportInterrupted TerminationCause = "transport_interrupted"

	// TerminationCauseUnknown means the provider supplied a termination reason
	// that this build does not recognize. The raw value is preserved alongside
	// it so it can be surfaced in logs instead of silently becoming "complete".
	TerminationCauseUnknown TerminationCause = "unknown"
)

// IsIncomplete reports whether the cause indicates the response is not a
// finished answer, and therefore should not be presented to the client as a
// clean completion.
func (c TerminationCause) IsIncomplete() bool {
	switch c {
	case TerminationCauseTokenLimit,
		TerminationCauseContextExhausted,
		TerminationCausePauseTurn,
		TerminationCauseMalformedToolCall,
		TerminationCauseMissingTerminal,
		TerminationCauseTransportInterrupted,
		TerminationCauseError,
		TerminationCauseUnknown:
		return true
	default:
		return false
	}
}

// IsProviderRefusal reports whether the provider deliberately withheld output.
// Retrying these on another credential or channel is usually pointless and can
// look like abuse, so relay policy excludes them from empty-output retries.
func (c TerminationCause) IsProviderRefusal() bool {
	switch c {
	case TerminationCauseContentFilter,
		TerminationCauseRecitation,
		TerminationCausePromptBlocked,
		TerminationCauseRefusal:
		return true
	default:
		return false
	}
}

// AllowsIdenticalReplay reports whether resending the exact same request could
// plausibly produce a different result. Context exhaustion and paused turns
// both fail this test: the former is deterministic, and the latter requires
// resuming with prior assistant content rather than starting over.
func (c TerminationCause) AllowsIdenticalReplay() bool {
	switch c {
	case TerminationCauseContextExhausted,
		TerminationCausePauseTurn,
		TerminationCauseMalformedToolCall,
		TerminationCauseUnknown:
		return false
	default:
		return true
	}
}

// IsProviderFailure reports terminal upstream outcomes that are neither a
// complete model turn nor a deliberate model refusal. Relay uses this to avoid
// recording a provider-declared failure as a successful request after it has
// been delivered to the client.
func (c TerminationCause) IsProviderFailure() bool {
	switch c {
	case TerminationCauseMalformedToolCall,
		TerminationCauseError,
		TerminationCauseMissingTerminal,
		TerminationCauseTransportInterrupted,
		TerminationCauseUnknown:
		return true
	default:
		return false
	}
}

// TerminationMetadata carries decoded termination detail that has no place in
// the OpenAI-shaped wire response. Every field is excluded from JSON so that
// adding detail here can never change what a client receives.
type TerminationMetadata struct {
	// Cause is the canonical, protocol-agnostic termination classification.
	Cause TerminationCause `json:"-"`

	// ProviderReason is the verbatim reason string from the upstream provider,
	// for example "MAX_TOKENS", "pause_turn", or "incomplete". Preserved so an
	// unrecognized value can be reported instead of discarded.
	ProviderReason string `json:"-"`

	// StopSequence is the specific stop sequence that was matched, when the
	// provider reports one.
	StopSequence string `json:"-"`

	// BlockReason is the prompt-level rejection reason, such as Gemini's
	// promptFeedback.blockReason, which arrives without any candidate.
	BlockReason string `json:"-"`

	// Detail is an optional provider-supplied elaboration, such as an OpenAI
	// Responses incomplete_details reason or an error message.
	Detail string `json:"-"`
}

// HasCause reports whether a canonical cause was decoded.
func (t TerminationMetadata) HasCause() bool {
	return t.Cause != TerminationCauseUnspecified
}
