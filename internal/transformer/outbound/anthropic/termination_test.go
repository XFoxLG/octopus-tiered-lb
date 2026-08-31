package anthropic

import (
	"context"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestAnthropicTerminationForStopReason(t *testing.T) {
	stopSequence := "<END>"
	testCases := []struct {
		name               string
		stopReason         string
		stopSequence       *string
		expectedCause      model.TerminationCause
		expectedWireFinish string
	}{
		{
			name:               "natural completion",
			stopReason:         "end_turn",
			expectedCause:      model.TerminationCauseComplete,
			expectedWireFinish: "stop",
		},
		{
			name:               "output token limit",
			stopReason:         "max_tokens",
			expectedCause:      model.TerminationCauseTokenLimit,
			expectedWireFinish: "length",
		},
		{
			name:               "configured stop sequence",
			stopReason:         "stop_sequence",
			stopSequence:       &stopSequence,
			expectedCause:      model.TerminationCauseStopSequence,
			expectedWireFinish: "stop",
		},
		{
			name:               "paused server turn",
			stopReason:         "pause_turn",
			expectedCause:      model.TerminationCausePauseTurn,
			expectedWireFinish: "length",
		},
		{
			name:               "context exhausted",
			stopReason:         "model_context_window_exceeded",
			expectedCause:      model.TerminationCauseContextExhausted,
			expectedWireFinish: "length",
		},
		{
			name:               "model refusal",
			stopReason:         "refusal",
			expectedCause:      model.TerminationCauseRefusal,
			expectedWireFinish: "content_filter",
		},
		{
			name:               "future provider reason",
			stopReason:         "future_reason",
			expectedCause:      model.TerminationCauseUnknown,
			expectedWireFinish: "length",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			termination := anthropicTerminationForStopReason(&testCase.stopReason, testCase.stopSequence)
			if termination.Cause != testCase.expectedCause {
				t.Fatalf("cause = %q, want %q", termination.Cause, testCase.expectedCause)
			}
			if termination.ProviderReason != testCase.stopReason {
				t.Fatalf("provider reason = %q, want %q", termination.ProviderReason, testCase.stopReason)
			}
			if testCase.stopSequence != nil && termination.StopSequence != *testCase.stopSequence {
				t.Fatalf("stop sequence = %q, want %q", termination.StopSequence, *testCase.stopSequence)
			}

			wireFinishReason := convertAnthropicFinishReason(termination.Cause)
			if wireFinishReason == nil || *wireFinishReason != testCase.expectedWireFinish {
				t.Fatalf("wire finish reason = %v, want %q", wireFinishReason, testCase.expectedWireFinish)
			}
		})
	}
}

func TestMessageOutboundTransformStreamPreservesPauseTurn(t *testing.T) {
	outbound := &MessageOutbound{}
	internalResponse, err := outbound.TransformStream(context.Background(), []byte(`{
		"type":"message_delta",
		"delta":{"stop_reason":"pause_turn"}
	}`))
	if err != nil {
		t.Fatalf("transform message delta: %v", err)
	}
	if internalResponse == nil || !internalResponse.SawTerminalEvent {
		t.Fatalf("terminal response = %#v, want terminal response", internalResponse)
	}
	if len(internalResponse.Choices) != 1 {
		t.Fatalf("choice count = %d, want 1", len(internalResponse.Choices))
	}

	choice := internalResponse.Choices[0]
	if choice.Termination.Cause != model.TerminationCausePauseTurn {
		t.Fatalf("termination cause = %q, want %q", choice.Termination.Cause, model.TerminationCausePauseTurn)
	}
	if choice.FinishReason == nil || *choice.FinishReason != "length" {
		t.Fatalf("finish reason = %v, want length", choice.FinishReason)
	}
}
