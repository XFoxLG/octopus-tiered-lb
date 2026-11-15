package relay

import (
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func TestStreamChunkClassificationSeparatesControlStructureAndContent(t *testing.T) {
	empty := &model.InternalLLMResponse{Choices: []model.Choice{{Index: 0, Delta: &model.Message{}}}}
	if !streamChunkIsEmptyControl(empty) || streamChunkStructure(empty) || streamChunkHasEffectiveOutput(empty) {
		t.Fatalf("empty delta classification = control:%v structure:%v output:%v",
			streamChunkIsEmptyControl(empty), streamChunkStructure(empty), streamChunkHasEffectiveOutput(empty))
	}

	role := &model.InternalLLMResponse{Choices: []model.Choice{{Index: 0, Delta: &model.Message{Role: "assistant"}}}}
	if streamChunkIsEmptyControl(role) || !streamChunkStructure(role) || streamChunkHasEffectiveOutput(role) {
		t.Fatalf("role classification = control:%v structure:%v output:%v",
			streamChunkIsEmptyControl(role), streamChunkStructure(role), streamChunkHasEffectiveOutput(role))
	}

	toolStart := &model.InternalLLMResponse{Choices: []model.Choice{{Index: 0, Delta: &model.Message{
		Role: "assistant",
		ToolCalls: []model.ToolCall{{
			ID:       "call-1",
			Type:     "function",
			Function: model.FunctionCall{Name: "lookup"},
		}},
	}}}}
	if streamChunkIsEmptyControl(toolStart) || !streamChunkStructure(toolStart) || streamChunkHasEffectiveOutput(toolStart) {
		t.Fatalf("tool start classification = control:%v structure:%v output:%v",
			streamChunkIsEmptyControl(toolStart), streamChunkStructure(toolStart), streamChunkHasEffectiveOutput(toolStart))
	}

	toolOutput := &model.InternalLLMResponse{Choices: []model.Choice{{Index: 0, Delta: &model.Message{
		ToolCalls: []model.ToolCall{{Function: model.FunctionCall{Arguments: `{"q":"x"}`}}},
	}}}}
	if streamChunkIsEmptyControl(toolOutput) || streamChunkStructure(toolOutput) || !streamChunkHasEffectiveOutput(toolOutput) {
		t.Fatalf("tool output classification = control:%v structure:%v output:%v",
			streamChunkIsEmptyControl(toolOutput), streamChunkStructure(toolOutput), streamChunkHasEffectiveOutput(toolOutput))
	}

	reasoning := "thinking"
	reasoningChunk := &model.InternalLLMResponse{Choices: []model.Choice{{Index: 0, Delta: &model.Message{
		Role:             "assistant",
		ReasoningContent: &reasoning,
	}}}}
	if streamChunkIsEmptyControl(reasoningChunk) || streamChunkStructure(reasoningChunk) || streamChunkHasEffectiveOutput(reasoningChunk) {
		t.Fatalf("reasoning classification = control:%v structure:%v output:%v",
			streamChunkIsEmptyControl(reasoningChunk), streamChunkStructure(reasoningChunk), streamChunkHasEffectiveOutput(reasoningChunk))
	}

	text := "answer"
	finish := "content_filter"
	mixedTerminal := &model.InternalLLMResponse{
		SawTerminalEvent: true,
		Choices: []model.Choice{{
			Index:        0,
			Delta:        &model.Message{Content: model.MessageContent{Content: &text}},
			FinishReason: &finish,
			Termination:  model.TerminationMetadata{Cause: model.TerminationCauseContentFilter},
		}},
	}
	if streamChunkIsEmptyControl(mixedTerminal) || streamChunkStructure(mixedTerminal) || !streamChunkHasEffectiveOutput(mixedTerminal) {
		t.Fatalf("mixed terminal classification = control:%v structure:%v output:%v",
			streamChunkIsEmptyControl(mixedTerminal), streamChunkStructure(mixedTerminal), streamChunkHasEffectiveOutput(mixedTerminal))
	}
}

func TestStreamChunkClassificationDoesNotTreatMetadataOrUsageAsVisibleOutput(t *testing.T) {
	metadata := &model.InternalLLMResponse{Object: "chat.completion.chunk", Model: "m"}
	if !streamChunkIsEmptyControl(metadata) || streamChunkHasEffectiveOutput(metadata) {
		t.Fatalf("metadata classification = control:%v output:%v",
			streamChunkIsEmptyControl(metadata), streamChunkHasEffectiveOutput(metadata))
	}

	usage := &model.InternalLLMResponse{Usage: &model.Usage{PromptTokens: 1, TotalTokens: 1}}
	if streamChunkIsEmptyControl(usage) || streamChunkHasEffectiveOutput(usage) {
		t.Fatalf("usage classification = control:%v output:%v",
			streamChunkIsEmptyControl(usage), streamChunkHasEffectiveOutput(usage))
	}

	terminal := &model.InternalLLMResponse{SawTerminalEvent: true, Object: "[DONE]"}
	if streamChunkIsEmptyControl(terminal) || streamChunkHasEffectiveOutput(terminal) {
		t.Fatalf("terminal classification = control:%v output:%v",
			streamChunkIsEmptyControl(terminal), streamChunkHasEffectiveOutput(terminal))
	}
}
