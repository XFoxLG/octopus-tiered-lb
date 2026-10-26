package relay

import (
	"context"
	"sort"
	"strings"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

// A few Chat-compatible bridges attach stop to every content delta. Such a
// choice is a finish candidate, not proof that the stream has stopped. Keep
// its metadata until DONE, a usage-only tail, an empty finish chunk, or EOF
// confirms the ending, and keep it out of converted downstream terminals.
func (ra *relayAttempt) deferChatFinishHints(stream *model.InternalLLMResponse) *model.InternalLLMResponse {
	if ra.adapterEndpointProtocol() != "chat" || stream.Object == "[DONE]" || stream.SawTerminalEvent || stream.Termination.HasCause() {
		return stream
	}
	var copy *model.InternalLLMResponse
	for index, choice := range stream.Choices {
		if !chatChoiceHasContentDelta(choice) {
			if choice.FinishReason != nil && strings.TrimSpace(*choice.FinishReason) != "" {
				delete(ra.chatPendingFinishChoices, choice.Index)
			}
			continue
		}
		// Continued content invalidates an earlier candidate for this choice.
		delete(ra.chatPendingFinishChoices, choice.Index)
		delete(ra.streamFinishedChoiceIndexes, choice.Index)
		if !ra.streamSawExplicitTerminalEvent && len(ra.streamFinishedChoiceIndexes) == 0 {
			ra.streamSawTerminalEvent = false
			ra.streamFinishReason = ""
			ra.streamTermination = model.TerminationMetadata{}
		}
		if choice.FinishReason == nil || strings.ToLower(strings.TrimSpace(*choice.FinishReason)) != "stop" || choice.Termination.Cause.IsIncomplete() || choice.Termination.Cause.IsProviderRefusal() || (choice.Delta != nil && choice.Delta.Refusal != "") {
			continue
		}
		if ra.chatPendingFinishChoices == nil {
			ra.chatPendingFinishChoices = make(map[int]model.Choice)
		}
		ra.chatPendingFinishChoices[choice.Index] = model.Choice{Index: choice.Index, FinishReason: choice.FinishReason, Termination: choice.Termination}
		if copy == nil {
			value := *stream
			value.Choices = append([]model.Choice(nil), stream.Choices...)
			copy = &value
		}
		copy.Choices[index].FinishReason = nil
		copy.Choices[index].Termination = model.TerminationMetadata{}
	}
	if copy != nil {
		return copy
	}
	return stream
}

func chatChoiceHasContentDelta(choice model.Choice) bool {
	delta := choice.Delta
	if delta == nil {
		return false
	}
	return (delta.Content.Content != nil && len(*delta.Content.Content) > 0) ||
		len(delta.Content.MultipleContent) > 0 || len(delta.Images) > 0 ||
		delta.GetReasoningContent() != "" || len(delta.ToolCalls) > 0 || delta.Audio != nil || delta.Refusal != ""
}

func (ra *relayAttempt) hasCompleteChatFinishHints() bool {
	if ra.adapterEndpointProtocol() != "chat" || len(ra.chatPendingFinishChoices) == 0 {
		return false
	}
	for index := range ra.streamObservedChoiceIndexes {
		if _, done := ra.streamFinishedChoiceIndexes[index]; done {
			continue
		}
		if _, hinted := ra.chatPendingFinishChoices[index]; !hinted {
			return false
		}
	}
	var request struct {
		N int `json:"n"`
	}
	if ra.internalRequest != nil {
		_ = jsonAPI.Unmarshal(ra.internalRequest.RawRequest, &request)
	}
	return request.N <= len(ra.streamObservedChoiceIndexes)
}

func (ra *relayAttempt) chatFinishHintsTerminal() *model.InternalLLMResponse {
	if len(ra.chatPendingFinishChoices) == 0 {
		return nil
	}
	terminal := &model.InternalLLMResponse{Object: "chat.completion.chunk"}
	for _, choice := range ra.chatPendingFinishChoices {
		terminal.Choices = append(terminal.Choices, choice)
	}
	sort.Slice(terminal.Choices, func(i, j int) bool { return terminal.Choices[i].Index < terminal.Choices[j].Index })
	return terminal
}

func (ra *relayAttempt) flushChatFinishHints(ctx context.Context) ([]byte, error) {
	terminal := ra.chatFinishHintsTerminal()
	if terminal == nil {
		return nil, nil
	}
	ra.chatPendingFinishChoices = nil
	ra.recordStreamTermination(terminal)
	if !ra.canPassthroughStream() {
		ra.extractLeadingInlineReasoningStream(terminal)
	}
	payload, err := ra.inAdapter.TransformStream(ctx, terminal)
	if ra.canPassthroughStream() {
		return nil, nil // Observation cannot synthesize or rewrite raw frames.
	}
	return payload, err
}
