package relay

import (
	"strings"
	"unicode/utf8"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

const maxInlineReasoningTagPrefix = 128

type inlineReasoningTag struct {
	open  string
	close string
}

func inlineReasoningTagList() []inlineReasoningTag {
	return []inlineReasoningTag{
		{open: "<thinking>", close: "</thinking>"},
		{open: "<think>", close: "</think>"},
	}
}

type inlineReasoningState struct {
	started    bool
	disabled   bool
	done       bool
	prefix     string
	closingTag string
	tail       string
	emitted    int
}

func (s *inlineReasoningState) observe(content string) (reasoningDelta, answer string, changed bool) {
	if s == nil || s.disabled || s.done {
		return "", content, false
	}
	if !s.started {
		s.prefix += content
		trimmed := strings.TrimLeft(s.prefix, " \r\n\t")
		if trimmed == "" {
			if s.prefix == "" {
				return "", "", false
			}
			return "", "", true
		}
		if tag, ok := matchInlineReasoningOpenTag(trimmed); ok {
			s.started = true
			s.closingTag = tag.close
			s.prefix = ""
			rest := trimmed[len(tag.open):]
			if rest == "" {
				return "", "", true
			}
			return s.appendStarted(rest)
		}
		if isInlineReasoningOpenPrefix(trimmed) && len(trimmed) <= maxInlineReasoningTagPrefix {
			return "", "", true
		}
		answer = s.prefix
		s.prefix = ""
		s.disabled = true
		return "", answer, true
	}
	return s.appendStarted(content)
}

func (s *inlineReasoningState) appendStarted(content string) (reasoningDelta, answer string, changed bool) {
	combined := s.tail + content
	if s.closingTag != "" {
		if index := strings.Index(combined, s.closingTag); index >= 0 {
			s.done = true
			delta := combined[:index]
			s.emitted += len(delta)
			s.tail = ""
			rest := strings.TrimLeft(combined[index+len(s.closingTag):], "\r\n")
			return delta, rest, true
		}
	}
	emit, hold := splitInlineReasoningHold(combined, s.closingTag)
	s.tail = hold
	s.emitted += len(emit)
	if emit == "" && content == "" {
		return "", "", false
	}
	return emit, "", true
}

func (s *inlineReasoningState) flush() (reasoningDelta, answer string, changed bool) {
	if s == nil || s.disabled || s.done {
		return "", "", false
	}
	if !s.started {
		answer = s.prefix
		s.prefix = ""
		s.disabled = true
		if answer == "" {
			return "", "", false
		}
		return "", answer, true
	}
	delta := s.tail
	s.tail = ""
	s.emitted += len(delta)
	s.done = true
	if delta == "" {
		return "", "", false
	}
	return delta, "", true
}

func matchInlineReasoningOpenTag(text string) (inlineReasoningTag, bool) {
	text = strings.TrimLeft(text, " \r\n\t")
	for _, tag := range inlineReasoningTagList() {
		if tag.open != "" && strings.HasPrefix(text, tag.open) {
			return tag, true
		}
	}
	return inlineReasoningTag{}, false
}

func isInlineReasoningOpenPrefix(text string) bool {
	if text == "" {
		return false
	}
	for _, tag := range inlineReasoningTagList() {
		if strings.HasPrefix(tag.open, text) {
			return true
		}
	}
	return false
}

func splitInlineReasoningHold(combined, closing string) (emit, hold string) {
	if closing == "" || combined == "" {
		return combined, ""
	}
	max := len(closing) - 1
	if max > len(combined) {
		max = len(combined)
	}
	for n := max; n > 0; n-- {
		if !utf8.ValidString(combined[:len(combined)-n]) {
			continue
		}
		suffix := combined[len(combined)-n:]
		if strings.HasPrefix(closing, suffix) {
			return combined[:len(combined)-n], suffix
		}
	}
	return combined, ""
}

func responseHasStructuredReasoning(resp *model.InternalLLMResponse) bool {
	if resp == nil {
		return false
	}
	for _, choice := range resp.Choices {
		for _, message := range []*model.Message{choice.Message, choice.Delta} {
			if message != nil && strings.TrimSpace(message.GetReasoningContent()) != "" {
				return true
			}
		}
	}
	return false
}

func extractLeadingInlineReasoning(resp *model.InternalLLMResponse) bool {
	if resp == nil || responseHasStructuredReasoning(resp) || len(resp.Choices) > 1 {
		return false
	}
	extracted := false
	for i := range resp.Choices {
		choice := &resp.Choices[i]
		message := choice.Message
		if message == nil || message.Content.Content == nil || len(message.Content.MultipleContent) != 0 {
			continue
		}
		state := &inlineReasoningState{}
		reasoning, answer, changed := state.observe(*message.Content.Content)
		if !state.started {
			continue
		}
		if !state.done {
			if flushReasoning, _, flushChanged := state.flush(); flushChanged {
				reasoning += flushReasoning
				changed = true
			}
		}
		if !changed {
			continue
		}
		if reasoning != "" {
			message.ReasoningContent = &reasoning
			extracted = true
		}
		answer = strings.TrimLeft(answer, "\r\n")
		message.Content.Content = &answer
	}
	return extracted
}

func (ra *relayAttempt) extractLeadingInlineReasoningStream(resp *model.InternalLLMResponse) {
	if ra == nil || resp == nil || responseHasStructuredReasoning(resp) || len(resp.Choices) > 1 {
		return
	}
	if ra.inlineReasoningStates == nil {
		ra.inlineReasoningStates = make(map[int]*inlineReasoningState)
	}
	for i := range resp.Choices {
		choice := &resp.Choices[i]
		terminal := choice.FinishReason != nil || choice.Termination.HasCause() || resp.Termination.HasCause()
		state := ra.inlineReasoningStates[choice.Index]
		if state == nil {
			state = &inlineReasoningState{}
			ra.inlineReasoningStates[choice.Index] = state
		}
		if state.disabled || state.done {
			continue
		}
		message := choice.Delta
		if message == nil {
			if !terminal {
				continue
			}
			message = &model.Message{}
			choice.Delta = message
		}
		content := ""
		if message.Content.Content != nil {
			content = *message.Content.Content
		}
		if content == "" && !terminal && !state.started && state.prefix == "" {
			continue
		}
		reasoning, answer, changed := state.observe(content)
		if terminal {
			if flushReasoning, flushAnswer, flushChanged := state.flush(); flushChanged {
				reasoning += flushReasoning
				answer += flushAnswer
				changed = true
			}
		}
		if !changed {
			continue
		}
		if reasoning != "" {
			ra.inlineReasoningExtracted = true
			if message.ReasoningContent == nil {
				message.ReasoningContent = &reasoning
			} else {
				*message.ReasoningContent += reasoning
			}
		}
		message.Content.Content = &answer
	}
}
