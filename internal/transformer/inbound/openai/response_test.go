package openai

import (
	"context"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestResponseInboundTransformStreamEmitsIncompleteForTokenLimit(t *testing.T) {
	finishReason := "length"
	inbound := &ResponseInbound{}
	streamPayload, err := inbound.TransformStream(context.Background(), &model.InternalLLMResponse{
		ID:      "resp_1",
		Model:   "gpt-test",
		Object:  "chat.completion.chunk",
		Choices: []model.Choice{{
			Index:        0,
			FinishReason: &finishReason,
			Termination: model.TerminationMetadata{
				Cause:          model.TerminationCauseTokenLimit,
				ProviderReason: "incomplete",
				Detail:         "max_tokens",
			},
		}},
	})
	if err != nil {
		t.Fatalf("transform stream: %v", err)
	}

	streamText := string(streamPayload)
	if !strings.Contains(streamText, `"type":"response.incomplete"`) {
		t.Fatalf("stream payload lacks response.incomplete: %s", streamText)
	}
	if !strings.Contains(streamText, `"status":"incomplete"`) {
		t.Fatalf("stream payload lacks incomplete status: %s", streamText)
	}
	if !strings.Contains(streamText, `"incomplete_details":{"reason":"max_tokens"}`) {
		t.Fatalf("stream payload lacks max_tokens detail: %s", streamText)
	}
	if strings.Contains(streamText, `"type":"response.completed"`) {
		t.Fatalf("token-limited stream incorrectly completed: %s", streamText)
	}
}

func TestResponseInboundTransformStreamEmitsFailedForError(t *testing.T) {
	finishReason := "error"
	inbound := &ResponseInbound{}
	streamPayload, err := inbound.TransformStream(context.Background(), &model.InternalLLMResponse{
		ID:      "resp_1",
		Model:   "gpt-test",
		Object:  "chat.completion.chunk",
		Choices: []model.Choice{{
			Index:        0,
			FinishReason: &finishReason,
			Termination: model.TerminationMetadata{
				Cause:  model.TerminationCauseError,
				Detail: "upstream failed",
			},
		}},
	})
	if err != nil {
		t.Fatalf("transform stream: %v", err)
	}

	streamText := string(streamPayload)
	if !strings.Contains(streamText, `"type":"response.failed"`) {
		t.Fatalf("stream payload lacks response.failed: %s", streamText)
	}
	if !strings.Contains(streamText, `"status":"failed"`) {
		t.Fatalf("stream payload lacks failed status: %s", streamText)
	}
	if strings.Contains(streamText, `"type":"response.completed"`) {
		t.Fatalf("failed stream incorrectly completed: %s", streamText)
	}
}

func TestResponseInboundPromptBlockStreamStaysEmptyAndIncomplete(t *testing.T) {
	inbound := &ResponseInbound{}
	streamPayload, err := inbound.TransformStream(context.Background(), &model.InternalLLMResponse{
		ID:     "resp_1",
		Model:  "gemini-test",
		Object: "chat.completion.chunk",
		Termination: model.TerminationMetadata{
			Cause: model.TerminationCausePromptBlocked,
		},
	})
	if err != nil {
		t.Fatalf("TransformStream() error = %v", err)
	}

	streamText := string(streamPayload)
	if !strings.Contains(streamText, `"type":"response.incomplete"`) {
		t.Fatalf("prompt-blocked stream lacks response.incomplete: %s", streamText)
	}
	if !strings.Contains(streamText, `"incomplete_details":{"reason":"content_filter"}`) {
		t.Fatalf("prompt-blocked stream lacks content_filter reason: %s", streamText)
	}
	if !strings.Contains(streamText, `"output":[]`) {
		t.Fatalf("prompt-blocked stream must retain empty output: %s", streamText)
	}
	if strings.Contains(streamText, `"type":"message"`) {
		t.Fatalf("prompt-blocked stream fabricated an assistant output item: %s", streamText)
	}
}

func TestConvertToResponsesAPIResponseUsesIncompleteStatus(t *testing.T) {
	response := convertToResponsesAPIResponse(&model.InternalLLMResponse{
		ID:    "resp_1",
		Model: "gpt-test",
		Termination: model.TerminationMetadata{
			Cause:  model.TerminationCauseTokenLimit,
			Detail: "max_tokens",
		},
	})
	if response.Status == nil || *response.Status != "incomplete" {
		t.Fatalf("status = %v, want incomplete", response.Status)
	}
	if response.IncompleteDetails == nil || response.IncompleteDetails.Reason != "max_tokens" {
		t.Fatalf("incomplete details = %#v, want max_tokens", response.IncompleteDetails)
	}
	if len(response.Output) != 0 {
		t.Fatalf("no-candidate incomplete response output = %#v, want empty", response.Output)
	}
}

func TestResponseInboundAggregationPreservesResponseTermination(t *testing.T) {
	inbound := &ResponseInbound{
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
