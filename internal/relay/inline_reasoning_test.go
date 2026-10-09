package relay

import (
	"strings"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/samber/lo"
)

func TestExtractLeadingInlineReasoningClosedThinkTag(t *testing.T) {
	resp := &model.InternalLLMResponse{Choices: []model.Choice{{
		Index: 0,
		Message: &model.Message{
			Content: model.MessageContent{Content: lo.ToPtr("<think>thought</think>\nanswer")},
		},
	}}}
	extractLeadingInlineReasoning(resp)
	if got := resp.Choices[0].Message.GetReasoningContent(); got != "thought" {
		t.Fatalf("reasoning = %q, want thought; content=%q", got, *resp.Choices[0].Message.Content.Content)
	}
	if got := *resp.Choices[0].Message.Content.Content; got != "answer" {
		t.Fatalf("answer = %q", got)
	}
}

func TestExtractLeadingInlineReasoningClosedThinkingTag(t *testing.T) {
	resp := &model.InternalLLMResponse{Choices: []model.Choice{{
		Index: 0,
		Message: &model.Message{
			Content: model.MessageContent{Content: lo.ToPtr("<thinking>workflow</thinking>answer")},
		},
	}}}
	extractLeadingInlineReasoning(resp)
	if got := resp.Choices[0].Message.GetReasoningContent(); got != "workflow" {
		t.Fatalf("reasoning = %q", got)
	}
	if got := *resp.Choices[0].Message.Content.Content; got != "answer" {
		t.Fatalf("answer = %q", got)
	}
}

func TestExtractLeadingInlineReasoningUnclosedOnComplete(t *testing.T) {
	resp := &model.InternalLLMResponse{Choices: []model.Choice{{
		Index: 0,
		Message: &model.Message{
			Content: model.MessageContent{Content: lo.ToPtr("<think>thought still thinking")},
		},
	}}}
	extractLeadingInlineReasoning(resp)
	if got := resp.Choices[0].Message.GetReasoningContent(); got != "thought still thinking" {
		t.Fatalf("reasoning = %q; content=%q", got, *resp.Choices[0].Message.Content.Content)
	}
	if got := strings.TrimSpace(*resp.Choices[0].Message.Content.Content); got != "" {
		t.Fatalf("answer = %q, want empty", got)
	}
}

func TestExtractLeadingInlineReasoningPreservesLaterTag(t *testing.T) {
	text := "answer discusses <think> literal"
	resp := &model.InternalLLMResponse{Choices: []model.Choice{{
		Index:   0,
		Message: &model.Message{Content: model.MessageContent{Content: &text}},
	}}}
	extractLeadingInlineReasoning(resp)
	if *resp.Choices[0].Message.Content.Content != text || resp.Choices[0].Message.GetReasoningContent() != "" {
		t.Fatalf("literal tag changed: %#v", resp.Choices[0].Message)
	}
}

func TestExtractLeadingInlineReasoningSkipsStructuredReasoning(t *testing.T) {
	resp := &model.InternalLLMResponse{Choices: []model.Choice{{
		Index: 0,
		Message: &model.Message{
			ReasoningContent: lo.ToPtr("structured"),
			Content:          model.MessageContent{Content: lo.ToPtr("<think>inline answer")},
		},
	}}}
	extractLeadingInlineReasoning(resp)
	if got := *resp.Choices[0].Message.Content.Content; got != "<think>inline answer" {
		t.Fatalf("answer changed: %q", got)
	}
}

func TestExtractLeadingInlineReasoningStreamSplitTag(t *testing.T) {
	ra := &relayAttempt{}
	chunks := []string{"<th", "inking>abc", "def</th", "inking>answer"}
	var reasoning strings.Builder
	var answer strings.Builder
	for _, chunk := range chunks {
		resp := &model.InternalLLMResponse{Choices: []model.Choice{{
			Index: 0,
			Delta: &model.Message{Content: model.MessageContent{Content: lo.ToPtr(chunk)}},
		}}}
		ra.extractLeadingInlineReasoningStream(resp)
		if got := resp.Choices[0].Delta.GetReasoningContent(); got != "" {
			reasoning.WriteString(got)
		}
		if got := resp.Choices[0].Delta.Content.Content; got != nil && *got != "" {
			answer.WriteString(*got)
		}
	}
	if reasoning.String() != "abcdef" {
		t.Fatalf("reasoning = %q, want abcdef", reasoning.String())
	}
	if answer.String() != "answer" {
		t.Fatalf("answer = %q", answer.String())
	}
}

func TestExtractLeadingInlineReasoningStreamUnclosedTerminal(t *testing.T) {
	ra := &relayAttempt{}
	resp := &model.InternalLLMResponse{
		Choices: []model.Choice{{
			Index: 0,
			Delta: &model.Message{Content: model.MessageContent{Content: lo.ToPtr("<think>partial reasoning")}},
		}},
	}
	ra.extractLeadingInlineReasoningStream(resp)
	terminal := &model.InternalLLMResponse{
		Choices: []model.Choice{{
			Index:        0,
			FinishReason: lo.ToPtr("stop"),
			Delta:        &model.Message{},
		}},
	}
	ra.extractLeadingInlineReasoningStream(terminal)
	if ra.inlineReasoningStates[0].emitted != len("partial reasoning") {
		t.Fatalf("unclosed stream reasoning not flushed: %#v", ra.inlineReasoningStates[0])
	}
}
