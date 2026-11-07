package model

import "testing"

func TestTerminationFromOpenAICompatFinishReason(t *testing.T) {
	for _, reason := range []string{"length", "max_tokens", "MAX_TOKENS"} {
		value := reason
		termination := TerminationFromOpenAICompatFinishReason(&value)
		if termination.Cause != TerminationCauseTokenLimit {
			t.Fatalf("finish_reason %q cause = %q, want token_limit", reason, termination.Cause)
		}
		if termination.ProviderReason != reason {
			t.Fatalf("finish_reason %q provider reason = %q", reason, termination.ProviderReason)
		}
	}

	stop := "stop"
	if termination := TerminationFromOpenAICompatFinishReason(&stop); termination.Cause != TerminationCauseComplete {
		t.Fatalf("stop cause = %q, want complete", termination.Cause)
	}
	if got := OpenAICompatFinishReasonForTermination(TerminationCauseTokenLimit); got != "length" {
		t.Fatalf("token-limit Chat finish = %q, want length", got)
	}
}
