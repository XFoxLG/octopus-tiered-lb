package model

import "testing"

func TestTerminationCauseReplaySafety(t *testing.T) {
	if !TerminationCauseComplete.AllowsIdenticalReplay() {
		t.Fatal("complete empty output should retain the existing retry policy")
	}
	for _, cause := range []TerminationCause{
		TerminationCauseContextExhausted,
		TerminationCausePauseTurn,
		TerminationCauseMalformedToolCall,
		TerminationCauseUnknown,
	} {
		if cause.AllowsIdenticalReplay() {
			t.Fatalf("%q must not allow an identical replay", cause)
		}
	}
}

func TestTerminationCauseProviderFailure(t *testing.T) {
	for _, cause := range []TerminationCause{
		TerminationCauseMalformedToolCall,
		TerminationCauseError,
		TerminationCauseMissingTerminal,
		TerminationCauseTransportInterrupted,
		TerminationCauseUnknown,
	} {
		if !cause.IsProviderFailure() {
			t.Fatalf("%q must be classified as a provider failure", cause)
		}
	}

	for _, cause := range []TerminationCause{
		TerminationCauseComplete,
		TerminationCauseTokenLimit,
		TerminationCauseContentFilter,
		TerminationCauseRefusal,
	} {
		if cause.IsProviderFailure() {
			t.Fatalf("%q must not be classified as a provider failure", cause)
		}
	}
}
