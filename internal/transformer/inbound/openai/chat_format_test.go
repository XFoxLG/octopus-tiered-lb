package openai

import (
	"context"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestChatInboundMarksOpenAIChatCompletionFormat(t *testing.T) {
	inbound := &ChatInbound{}

	req, err := inbound.TransformRequest(context.Background(), []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("TransformRequest() error = %v", err)
	}
	if req.RawAPIFormat != model.APIFormatOpenAIChatCompletion {
		t.Fatalf("RawAPIFormat = %q, want %q", req.RawAPIFormat, model.APIFormatOpenAIChatCompletion)
	}
}

func strPtr(s string) *string { return &s }

func TestChatInboundTransformStreamFinishReasonChunkHasDelta(t *testing.T) {
	// Reproduces issue #62: the terminal finish_reason chunk must carry an
	// empty "delta":{} so strict clients parsing choices[0].delta don't get null.
	inbound := &ChatInbound{}
	chunk := &model.InternalLLMResponse{
		ID:      "resp-id",
		Object:  "chat.completion.chunk",
		Created: 1,
		Model:   "claude-sonnet-4-6",
		Choices: []model.Choice{
			{
				Index:        0,
				FinishReason: strPtr("stop"),
			},
		},
	}

	out, err := inbound.TransformStream(context.Background(), chunk)
	if err != nil {
		t.Fatalf("TransformStream() error = %v", err)
	}
	body := string(out)

	if !strings.HasPrefix(body, "data: ") {
		t.Fatalf("TransformStream() output missing SSE data prefix: %q", body)
	}
	// Accept both encoding/json (delta:{}) and jsoniter (delta:{"content":null}) output
	if !strings.Contains(body, `"delta":{}`) && !strings.Contains(body, `"delta":{"content":null}`) {
		t.Fatalf("TransformStream() finish_reason chunk missing delta; got %q", body)
	}
	if !strings.Contains(body, `"finish_reason":"stop"`) {
		t.Fatalf("TransformStream() missing finish_reason; got %q", body)
	}

	// The stored/internal chunk must not be mutated — Delta should still be nil
	// so aggregation semantics are unchanged.
	if inbound.streamChunks[0].Choices[0].Delta != nil {
		t.Fatalf("TransformStream() mutated input chunk Delta to non-nil")
	}
}

func TestChatInboundTransformStreamEmptyChoicesIsArray(t *testing.T) {
	// Cherry Studio et al. require "choices" to always be present as an array.
	inbound := &ChatInbound{}
	chunk := &model.InternalLLMResponse{
		ID:      "resp-id",
		Object:  "chat.completion.chunk",
		Created: 1,
		Model:   "gpt-test",
		Choices: nil,
	}

	out, err := inbound.TransformStream(context.Background(), chunk)
	if err != nil {
		t.Fatalf("TransformStream() error = %v", err)
	}
	if !strings.Contains(string(out), `"choices":[]`) {
		t.Fatalf("TransformStream() empty choices not serialized as []; got %q", out)
	}
}

func TestChatInboundMaterializesPromptBlockAsTerminalChoice(t *testing.T) {
	promptBlockedResponse := &model.InternalLLMResponse{
		ID:      "resp-id",
		Object:  "chat.completion",
		Created: 1,
		Model:   "gemini-test",
		Termination: model.TerminationMetadata{
			Cause:          model.TerminationCausePromptBlocked,
			ProviderReason: "SAFETY",
		},
	}

	responseInbound := &ChatInbound{}
	responsePayload, err := responseInbound.TransformResponse(context.Background(), promptBlockedResponse)
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}
	if !strings.Contains(string(responsePayload), `"finish_reason":"content_filter"`) {
		t.Fatalf("TransformResponse() missing content_filter terminal choice: %s", responsePayload)
	}
	if len(promptBlockedResponse.Choices) != 0 {
		t.Fatal("TransformResponse() mutated no-candidate provider response")
	}

	streamInbound := &ChatInbound{}
	promptBlockedStream := *promptBlockedResponse
	promptBlockedStream.Object = "chat.completion.chunk"
	streamPayload, err := streamInbound.TransformStream(context.Background(), &promptBlockedStream)
	if err != nil {
		t.Fatalf("TransformStream() error = %v", err)
	}
	streamText := string(streamPayload)
	if !strings.Contains(streamText, `"finish_reason":"content_filter"`) {
		t.Fatalf("TransformStream() missing content_filter terminal choice: %s", streamText)
	}
	if !strings.Contains(streamText, `"delta":{}`) && !strings.Contains(streamText, `"delta":{"content":null}`) {
		t.Fatalf("TransformStream() terminal choice lacks empty delta: %s", streamText)
	}
	if len(promptBlockedStream.Choices) != 0 {
		t.Fatal("TransformStream() mutated no-candidate provider response")
	}
}

func TestChatInboundAggregationPreservesResponseTermination(t *testing.T) {
	inbound := &ChatInbound{
		streamChunks: []*model.InternalLLMResponse{{
			Object:           "chat.completion.chunk",
			SawTerminalEvent: true,
			Termination: model.TerminationMetadata{
				Cause: model.TerminationCausePromptBlocked,
			},
		}},
	}

	response, err := inbound.GetInternalResponse(context.Background())
	if err != nil {
		t.Fatalf("GetInternalResponse() error = %v", err)
	}
	if response == nil || response.Termination.Cause != model.TerminationCausePromptBlocked {
		t.Fatalf("response termination = %#v, want prompt_blocked", response)
	}
	if !response.SawTerminalEvent {
		t.Fatal("response-level terminal marker was lost during aggregation")
	}
}

func TestChatInboundTransformStreamDeltaChunkPreserved(t *testing.T) {
	// A normal content delta chunk must still serialize its delta payload.
	inbound := &ChatInbound{}
	chunk := &model.InternalLLMResponse{
		ID:      "resp-id",
		Object:  "chat.completion.chunk",
		Created: 1,
		Model:   "gpt-test",
		Choices: []model.Choice{
			{
				Index: 0,
				Delta: &model.Message{
					Role: "assistant",
					Content: model.MessageContent{
						Content: strPtr("hello"),
					},
				},
			},
		},
	}

	out, err := inbound.TransformStream(context.Background(), chunk)
	if err != nil {
		t.Fatalf("TransformStream() error = %v", err)
	}
	body := string(out)
	if !strings.Contains(body, `"content":"hello"`) {
		t.Fatalf("TransformStream() delta content lost; got %q", body)
	}
	if strings.Contains(body, `"finish_reason"`) {
		t.Fatalf("TransformStream() unexpected finish_reason on delta chunk; got %q", body)
	}
}
