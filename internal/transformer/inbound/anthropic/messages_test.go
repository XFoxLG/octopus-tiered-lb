package anthropic

import (
	"context"
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer"
	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestMessagesInboundGetInternalResponsePreservesSparseChoiceIndexes(t *testing.T) {
	first := "first"
	second := "second"
	inbound := &MessagesInbound{
		streamChunks: []*model.InternalLLMResponse{
			{
				ID:      "resp-id",
				Object:  "chat.completion.chunk",
				Created: 1,
				Model:   "claude-test",
				Choices: []model.Choice{
					{
						Index: 2,
						Delta: &model.Message{
							Role: "assistant",
							Content: model.MessageContent{
								Content: &second,
							},
						},
					},
				},
			},
			{
				ID:      "resp-id",
				Object:  "chat.completion.chunk",
				Created: 1,
				Model:   "claude-test",
				Choices: []model.Choice{
					{
						Index: 1,
						Delta: &model.Message{
							Role: "assistant",
							Content: model.MessageContent{
								Content: &first,
							},
						},
					},
				},
			},
		},
	}

	resp, err := inbound.GetInternalResponse(context.Background())
	if err != nil {
		t.Fatalf("GetInternalResponse() error = %v", err)
	}
	if resp == nil {
		t.Fatal("GetInternalResponse() response = nil")
	}
	if len(resp.Choices) != 2 {
		t.Fatalf("GetInternalResponse() choices len = %d, want 2", len(resp.Choices))
	}
	if resp.Choices[0].Index != 1 || resp.Choices[1].Index != 2 {
		t.Fatalf("GetInternalResponse() indexes = [%d %d], want [1 2]", resp.Choices[0].Index, resp.Choices[1].Index)
	}
	if resp.Choices[0].Message == nil || resp.Choices[0].Message.Content.Content == nil || *resp.Choices[0].Message.Content.Content != first {
		t.Fatalf("GetInternalResponse() first content = %+v, want %q", resp.Choices[0].Message, first)
	}
	if resp.Choices[1].Message == nil || resp.Choices[1].Message.Content.Content == nil || *resp.Choices[1].Message.Content.Content != second {
		t.Fatalf("GetInternalResponse() second content = %+v, want %q", resp.Choices[1].Message, second)
	}
}

func TestMessagesInboundTransformStreamFinishesWithoutUsage(t *testing.T) {
	finishReason := "length"
	inbound := &MessagesInbound{}
	streamPayload, err := inbound.TransformStream(context.Background(), &model.InternalLLMResponse{
		Object: "chat.completion.chunk",
		Choices: []model.Choice{{
			Index:        0,
			FinishReason: &finishReason,
			Termination: model.TerminationMetadata{
				Cause: model.TerminationCausePauseTurn,
			},
		}},
	})
	if err != nil {
		t.Fatalf("transform stream: %v", err)
	}

	streamText := string(streamPayload)
	if !strings.Contains(streamText, `"type":"message_delta"`) {
		t.Fatalf("stream payload lacks message_delta: %s", streamText)
	}
	if !strings.Contains(streamText, `"stop_reason":"pause_turn"`) {
		t.Fatalf("stream payload lacks pause_turn: %s", streamText)
	}
	if !strings.Contains(streamText, `"type":"message_stop"`) {
		t.Fatalf("stream payload lacks message_stop: %s", streamText)
	}
}

func TestMessagesInboundTransformResponsePreservesStopSequence(t *testing.T) {
	finishReason := "stop"
	inbound := &MessagesInbound{}
	responsePayload, err := inbound.TransformResponse(context.Background(), &model.InternalLLMResponse{
		Object: "chat.completion",
		Choices: []model.Choice{{
			Index:        0,
			FinishReason: &finishReason,
			Termination: model.TerminationMetadata{
				Cause:        model.TerminationCauseStopSequence,
				StopSequence: "<END>",
			},
		}},
	})
	if err != nil {
		t.Fatalf("transform response: %v", err)
	}

	var anthropicResponse Message
	if err := transformer.Unmarshal(responsePayload, &anthropicResponse); err != nil {
		t.Fatalf("decode response payload: %v", err)
	}
	if anthropicResponse.StopReason == nil || *anthropicResponse.StopReason != "stop_sequence" {
		t.Fatalf("stop reason = %v, want stop_sequence", anthropicResponse.StopReason)
	}
	if anthropicResponse.StopSequence == nil || *anthropicResponse.StopSequence != "<END>" {
		t.Fatalf("stop sequence = %v, want <END>", anthropicResponse.StopSequence)
	}
}

func TestMessagesInboundPromptBlockUsesEmptyContentArray(t *testing.T) {
	inbound := &MessagesInbound{}
	responsePayload, err := inbound.TransformResponse(context.Background(), &model.InternalLLMResponse{
		ID:     "msg_1",
		Model:  "gemini-test",
		Object: "chat.completion",
		Termination: model.TerminationMetadata{
			Cause: model.TerminationCausePromptBlocked,
		},
	})
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}

	responseText := string(responsePayload)
	if !strings.Contains(responseText, `"content":[]`) {
		t.Fatalf("prompt-blocked response content = %s, want empty array", responseText)
	}
	if strings.Contains(responseText, `"content":null`) {
		t.Fatalf("prompt-blocked response contains invalid null content: %s", responseText)
	}
	if !strings.Contains(responseText, `"stop_reason":"refusal"`) {
		t.Fatalf("prompt-blocked response missing refusal stop reason: %s", responseText)
	}
}

func TestMessagesInboundPromptBlockStreamTerminatesWithoutText(t *testing.T) {
	inbound := &MessagesInbound{}
	streamPayload, err := inbound.TransformStream(context.Background(), &model.InternalLLMResponse{
		Object: "chat.completion.chunk",
		Termination: model.TerminationMetadata{
			Cause: model.TerminationCausePromptBlocked,
		},
	})
	if err != nil {
		t.Fatalf("TransformStream() error = %v", err)
	}

	streamText := string(streamPayload)
	if !strings.Contains(streamText, `"type":"message_start"`) {
		t.Fatalf("prompt-blocked stream lacks message_start: %s", streamText)
	}
	if !strings.Contains(streamText, `"type":"message_delta"`) || !strings.Contains(streamText, `"stop_reason":"refusal"`) {
		t.Fatalf("prompt-blocked stream lacks refusal terminal delta: %s", streamText)
	}
	if !strings.Contains(streamText, `"type":"message_stop"`) {
		t.Fatalf("prompt-blocked stream lacks message_stop: %s", streamText)
	}
}

func TestMessagesInboundStreamRendersProviderFailureAsErrorEvent(t *testing.T) {
	inbound := &MessagesInbound{}
	streamPayload, err := inbound.TransformStream(context.Background(), &model.InternalLLMResponse{
		Object: "chat.completion.chunk",
		Termination: model.TerminationMetadata{
			Cause:  model.TerminationCauseError,
			Detail: "upstream response failed",
		},
	})
	if err != nil {
		t.Fatalf("TransformStream() error = %v", err)
	}

	streamText := string(streamPayload)
	if !strings.Contains(streamText, "event:error") {
		t.Fatalf("provider failure lacks Anthropic error event: %s", streamText)
	}
	if !strings.Contains(streamText, `"type":"error"`) || !strings.Contains(streamText, `"type":"api_error"`) {
		t.Fatalf("provider failure has invalid Anthropic error payload: %s", streamText)
	}
	if strings.Contains(streamText, `"stop_reason":"refusal"`) || strings.Contains(streamText, `"type":"message_stop"`) {
		t.Fatalf("provider failure was incorrectly rendered as a model refusal: %s", streamText)
	}
	if !strings.Contains(streamText, `"request_id":null`) {
		t.Fatalf("provider failure lacks Anthropic null request_id: %s", streamText)
	}
}

func TestMessagesInboundStreamRendersErrorFinishReasonAsErrorEvent(t *testing.T) {
	finishReason := "error"
	inbound := &MessagesInbound{}
	streamPayload, err := inbound.TransformStream(context.Background(), &model.InternalLLMResponse{
		Object: "chat.completion.chunk",
		Choices: []model.Choice{{
			FinishReason: &finishReason,
		}},
	})
	if err != nil {
		t.Fatalf("TransformStream() error = %v", err)
	}

	streamText := string(streamPayload)
	if !strings.Contains(streamText, "event:error") || !strings.Contains(streamText, `"type":"api_error"`) {
		t.Fatalf("error finish reason lacks Anthropic error event: %s", streamText)
	}
	if strings.Contains(streamText, `"stop_reason":"refusal"`) || strings.Contains(streamText, `"type":"message_stop"`) {
		t.Fatalf("error finish reason was rendered as a model terminal: %s", streamText)
	}
}

func TestMessagesInboundRendersTransportInterruptionAsErrorEvent(t *testing.T) {
	inbound := &MessagesInbound{}
	streamPayload, err := inbound.TransformStreamInterruption(context.Background(), context.DeadlineExceeded)
	if err != nil {
		t.Fatalf("TransformStreamInterruption() error = %v", err)
	}

	streamText := string(streamPayload)
	if !strings.Contains(streamText, "event:error") || !strings.Contains(streamText, `"type":"api_error"`) {
		t.Fatalf("transport interruption lacks Anthropic API error envelope: %s", streamText)
	}
	if !strings.Contains(streamText, "context deadline exceeded") {
		t.Fatalf("transport interruption message missing: %s", streamText)
	}
}

func TestMessagesInboundAggregationPreservesResponseTermination(t *testing.T) {
	inbound := &MessagesInbound{
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
