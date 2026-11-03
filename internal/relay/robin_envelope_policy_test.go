package relay

import (
	"errors"
	"net/http"
	"testing"

	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestRobinInsufficientOutputDecision(t *testing.T) {
	tests := []struct {
		name         string
		written      bool
		retryEnabled bool
		wantScope    RetryScope
		wantReason   string
	}{
		{
			name:         "retries before output is committed",
			retryEnabled: true,
			wantScope:    ScopeSameChannel,
			wantReason:   "empty internal tool envelope, try another key",
		},
		{
			name:       "does not retry when empty-output retry is disabled",
			wantScope:  ScopeNone,
			wantReason: "empty internal tool envelope, retry disabled",
		},
		{
			name:         "does not retry after output is committed",
			written:      true,
			retryEnabled: true,
			wantScope:    ScopeNone,
			wantReason:   "empty internal tool envelope after output was committed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := robinInsufficientOutputDecision(test.written, test.retryEnabled, http.StatusOK)
			if decision.Scope != test.wantScope {
				t.Fatalf("scope = %s, want %s", decision.Scope, test.wantScope)
			}
			if decision.Reason != test.wantReason {
				t.Fatalf("reason = %q, want %q", decision.Reason, test.wantReason)
			}
			if decision.Code != http.StatusOK || !decision.IsError {
				t.Fatalf("decision = %#v, want HTTP 200 error decision", decision)
			}
		})
	}
}

func TestApplyRobinEnvelopePolicyToResponse(t *testing.T) {
	tests := []struct {
		name         string
		message      *transmodel.Message
		declared     []transmodel.Tool
		extract      bool
		wantErr      error
		wantContent  string
		wantFinish   string
		wantToolsLen int
	}{
		{
			name: "empty dispatch is an insufficient output",
			message: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
				Type: "function",
				Function: transmodel.FunctionCall{
					Name:      "dispatch_stream_payload_0123456789abcdef",
					Arguments: "{}",
				},
			}}},
			wantErr:      errRobinInsufficientOutput,
			wantToolsLen: 1,
		},
		{
			name: "declared actual_content remains a tool call",
			message: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
				Type: "function",
				Function: transmodel.FunctionCall{
					Name:      "actual_content",
					Arguments: `{"content":"visible"}`,
				},
			}}},
			declared:     []transmodel.Tool{{Type: "function", Function: transmodel.Function{Name: "actual_content"}}},
			extract:      true,
			wantToolsLen: 1,
			wantFinish:   "tool_calls",
		},
		{
			name: "extraction disabled preserves actual_content",
			message: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
				Type: "function",
				Function: transmodel.FunctionCall{
					Name:      "actual_content",
					Arguments: `{"content":"visible"}`,
				},
			}}},
			extract:      false,
			wantToolsLen: 1,
			wantFinish:   "tool_calls",
		},
		{
			name: "extraction enabled converts strict actual_content",
			message: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
				Type: "function",
				Function: transmodel.FunctionCall{
					Name:      "actual_content",
					Arguments: `{"content":"visible"}`,
				},
			}}},
			extract:      true,
			wantContent:  "visible",
			wantFinish:   "stop",
			wantToolsLen: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			finish := "tool_calls"
			response := &transmodel.InternalLLMResponse{Choices: []transmodel.Choice{{
				Message:      test.message,
				FinishReason: &finish,
				Termination:  transmodel.TerminationMetadata{Cause: transmodel.TerminationCauseToolCall},
			}}}
			attempt := newRobinPolicyTestAttempt(test.declared)

			err := attempt.applyRobinEnvelopePolicyToResponseWithExtraction(response, test.extract)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			choice := response.Choices[0]
			if got := len(choice.Message.ToolCalls); got != test.wantToolsLen {
				t.Fatalf("tool calls = %d, want %d", got, test.wantToolsLen)
			}
			if test.wantContent != "" {
				if choice.Message.Content.Content == nil || *choice.Message.Content.Content != test.wantContent {
					t.Fatalf("content = %#v, want %q", choice.Message.Content.Content, test.wantContent)
				}
			}
			if test.wantFinish != "" {
				if choice.FinishReason == nil || *choice.FinishReason != test.wantFinish {
					t.Fatalf("finish reason = %#v, want %q", choice.FinishReason, test.wantFinish)
				}
			}
		})
	}
}

func TestApplyRobinEnvelopePolicyKeepsNormalChoiceAlongsideEmptyDispatch(t *testing.T) {
	finish := "tool_calls"
	response := &transmodel.InternalLLMResponse{Choices: []transmodel.Choice{
		{
			Index: 0,
			Message: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
				Type: "function",
				Function: transmodel.FunctionCall{
					Name:      "dispatch_stream_payload_0123456789abcdef",
					Arguments: "{}",
				},
			}}},
			FinishReason: &finish,
		},
		{
			Index:        1,
			Message:      &transmodel.Message{Content: transmodel.MessageContent{Content: robinStringPtr("visible")}},
			FinishReason: robinStringPtr("stop"),
		},
	}}
	attempt := newRobinPolicyTestAttempt(nil)

	if err := attempt.applyRobinEnvelopePolicyToResponseWithExtraction(response, false); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if response.Choices[1].Message.Content.Content == nil || *response.Choices[1].Message.Content.Content != "visible" {
		t.Fatalf("normal choice was discarded or rewritten: %#v", response.Choices[1])
	}
}

func TestApplyRobinEnvelopePolicyDefersToProviderFailure(t *testing.T) {
	finish := "error"
	response := &transmodel.InternalLLMResponse{Choices: []transmodel.Choice{{
		Message: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
			Type: "function",
			Function: transmodel.FunctionCall{
				Name:      "dispatch_stream_payload_0123456789abcdef",
				Arguments: "{}",
			},
		}}},
		FinishReason: &finish,
		Termination: transmodel.TerminationMetadata{
			Cause:          transmodel.TerminationCauseError,
			ProviderReason: finish,
		},
	}}}
	attempt := newRobinPolicyTestAttempt(nil)

	if err := attempt.applyRobinEnvelopePolicyToResponseWithExtraction(response, true); err != nil {
		t.Fatalf("provider failure was replaced by Robin retry error: %v", err)
	}
	if len(response.Choices[0].Message.ToolCalls) != 1 {
		t.Fatal("provider failure response was rewritten")
	}
}

func TestApplyRobinEnvelopePolicyDefersToProviderRefusal(t *testing.T) {
	finish := "content_filter"
	response := &transmodel.InternalLLMResponse{Choices: []transmodel.Choice{{
		Message: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
			Type: "function",
			Function: transmodel.FunctionCall{
				Name:      "dispatch_stream_payload_0123456789abcdef",
				Arguments: "{}",
			},
		}}},
		FinishReason: &finish,
		Termination: transmodel.TerminationMetadata{
			Cause:          transmodel.TerminationCauseContentFilter,
			ProviderReason: finish,
		},
	}}}
	attempt := newRobinPolicyTestAttempt(nil)

	if err := attempt.applyRobinEnvelopePolicyToResponseWithExtraction(response, true); err != nil {
		t.Fatalf("provider refusal was replaced by Robin retry error: %v", err)
	}
	if len(response.Choices[0].Message.ToolCalls) != 1 {
		t.Fatal("provider refusal response was rewritten")
	}
}

func TestApplyRobinEnvelopePolicyToStreamChunk(t *testing.T) {
	terminal := "tool_calls"
	chunk := &transmodel.InternalLLMResponse{Choices: []transmodel.Choice{{
		Delta: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
			Type: "function",
			Function: transmodel.FunctionCall{
				Name:      "dispatch_stream_payload_0123456789abcdef",
				Arguments: "{}",
			},
		}}},
		FinishReason: &terminal,
	}}}
	attempt := newRobinPolicyTestAttempt(nil)

	if err := attempt.applyRobinEnvelopePolicyToStreamChunkWithExtraction(chunk, false); !errors.Is(err, errRobinInsufficientOutput) {
		t.Fatalf("error = %v, want errRobinInsufficientOutput", err)
	}
}

func TestApplyRobinEnvelopePolicyStreamingMultiChoiceDoesNotAbort(t *testing.T) {
	finish := "tool_calls"
	chunk := &transmodel.InternalLLMResponse{Choices: []transmodel.Choice{
		{
			Index: 0,
			Delta: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
				Type: "function",
				Function: transmodel.FunctionCall{
					Name:      "dispatch_stream_payload_0123456789abcdef",
					Arguments: "{}",
				},
			}}},
			FinishReason: &finish,
		},
		{
			Index:        1,
			Delta:        &transmodel.Message{Content: transmodel.MessageContent{Content: robinStringPtr("visible")}},
			FinishReason: robinStringPtr("stop"),
		},
	}}
	attempt := newRobinPolicyTestAttempt(nil)

	if err := attempt.applyRobinEnvelopePolicyToStreamChunkWithExtraction(chunk, false); err != nil {
		t.Fatalf("multi-choice stream was aborted: %v", err)
	}
}

func TestApplyRobinEnvelopePolicyToStreamChunkExtractsActualContent(t *testing.T) {
	terminal := "tool_calls"
	chunk := &transmodel.InternalLLMResponse{Choices: []transmodel.Choice{{
		Delta: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
			Type: "function",
			Function: transmodel.FunctionCall{
				Name:      "actual_content",
				Arguments: `{"content":"visible"}`,
			},
		}}},
		FinishReason: &terminal,
	}}}
	attempt := newRobinPolicyTestAttempt(nil)

	if err := attempt.applyRobinEnvelopePolicyToStreamChunkWithExtraction(chunk, true); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	choice := chunk.Choices[0]
	if choice.Delta.Content.Content == nil || *choice.Delta.Content.Content != "visible" {
		t.Fatalf("content = %#v, want visible", choice.Delta.Content.Content)
	}
	if len(choice.Delta.ToolCalls) != 0 {
		t.Fatalf("tool calls = %d, want 0", len(choice.Delta.ToolCalls))
	}
	if choice.FinishReason == nil || *choice.FinishReason != "stop" {
		t.Fatalf("finish reason = %#v, want stop", choice.FinishReason)
	}
}

func TestApplyRobinEnvelopePolicyIgnoresNonTerminalToolFragment(t *testing.T) {
	chunk := &transmodel.InternalLLMResponse{Choices: []transmodel.Choice{{
		Delta: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
			Type: "function",
			Function: transmodel.FunctionCall{
				Name:      "dispatch_stream_payload_0123456789abcdef",
				Arguments: "{}",
			},
		}}},
	}}}
	attempt := newRobinPolicyTestAttempt(nil)

	if err := attempt.applyRobinEnvelopePolicyToStreamChunkWithExtraction(chunk, false); err != nil {
		t.Fatalf("non-terminal fragment error = %v, want nil", err)
	}
}

func TestApplyRobinEnvelopePolicyDoesNotRetryEmptyDispatchAfterVisibleOutput(t *testing.T) {
	terminal := "tool_calls"
	chunk := &transmodel.InternalLLMResponse{Choices: []transmodel.Choice{{
		Delta: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
			Type: "function",
			Function: transmodel.FunctionCall{
				Name:      "dispatch_stream_payload_0123456789abcdef",
				Arguments: "{}",
			},
		}}},
		FinishReason: &terminal,
	}}}
	attempt := newRobinPolicyTestAttempt(nil)
	attempt.markStreamOutputCommitted()

	if err := attempt.applyRobinEnvelopePolicyToStreamChunkWithExtraction(chunk, false); err != nil {
		t.Fatalf("committed stream error = %v, want nil", err)
	}
}

func TestApplyRobinEnvelopePolicyDoesNotRewriteRawForwarding(t *testing.T) {
	finish := "tool_calls"
	response := &transmodel.InternalLLMResponse{Choices: []transmodel.Choice{{
		Message: &transmodel.Message{ToolCalls: []transmodel.ToolCall{{
			Type: "function",
			Function: transmodel.FunctionCall{
				Name:      "actual_content",
				Arguments: `{"content":"visible"}`,
			},
		}}},
		FinishReason: &finish,
	}}}
	attempt := newRobinPolicyTestAttempt(nil)
	attempt.adapterType = outbound.OutboundTypeRaw

	if err := attempt.applyRobinEnvelopePolicyToResponseWithExtraction(response, true); err != nil {
		t.Fatalf("raw forwarding error = %v, want nil", err)
	}
	if len(response.Choices[0].Message.ToolCalls) != 1 ||
		response.Choices[0].Message.Content.Content != nil {
		t.Fatalf("raw forwarding response was rewritten: %#v", response.Choices[0])
	}
}

func newRobinPolicyTestAttempt(tools []transmodel.Tool) *relayAttempt {
	request := &transmodel.InternalLLMRequest{Tools: tools}
	return &relayAttempt{
		relayRequest: &relayRequest{
			internalRequest: request,
			metrics:         NewRelayMetrics(0, "requested", "chat", "chat", "", "", request),
		},
		adapterType:      outbound.OutboundTypeOpenAIChat,
		logAttemptNumber: 1,
	}
}
