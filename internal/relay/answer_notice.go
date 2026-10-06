package relay

import (
	"io"
	"regexp"
	"strings"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

const answerNoticeLimit = 4 << 10
const googleNoticeStart = "The prompt could not be submitted."

type prefixResponseBody struct {
	io.Reader
	io.Closer
}

var googleNoticePattern = regexp.MustCompile("^The prompt could not be submitted\\. The prompt contains sensitive words that violate Google's (?:Generative AI Prohibited Use policy|\\[Generative AI Prohibited Use policy\\]\\(https://policies\\.google\\.com/terms/generative-ai/use-policy\\))\\. Try rephrasing the prompt\\.(?: If you think this was an error, (?:send feedback|\\[send feedback\\]\\(https://ai\\.google\\.dev/gemini-api/docs/troubleshooting\\))\\.)?$")

func isExplicitAnswerNotice(text string) bool {
	if len(text) > answerNoticeLimit {
		return false
	}
	text = strings.ReplaceAll(strings.TrimSpace(text), "’", "'")
	return googleNoticePattern.MatchString(text)
}

func answerText(response *model.InternalLLMResponse, stream bool) (string, bool) {
	if response == nil || len(response.Choices) > 1 {
		return "", response != nil && len(response.Choices) > 1
	}
	var text strings.Builder
	for _, choice := range response.Choices {
		message := choice.Message
		if stream {
			message = choice.Delta
		}
		if message == nil {
			continue
		}
		if len(message.ToolCalls) > 0 || message.Audio != nil {
			return "", true
		}
		for _, part := range message.Content.MultipleContent {
			if part.Type != "text" || part.Text == nil || text.Len()+len(*part.Text) > answerNoticeLimit {
				return "", true
			}
			text.WriteString(*part.Text)
		}
		if message.Content.Content != nil {
			if text.Len()+len(*message.Content.Content) > answerNoticeLimit {
				return "", true
			}
			text.WriteString(*message.Content.Content)
		}
	}
	return text.String(), false
}

func answerNoticeTermination() model.TerminationMetadata {
	return model.TerminationMetadata{
		Cause:  model.TerminationCauseContentFilter,
		Detail: "The prompt could not be submitted: content_filter (upstream policy notice).",
	}
}

func responseContentBlock(response *model.InternalLLMResponse) (model.TerminationMetadata, bool) {
	if response == nil {
		return model.TerminationMetadata{}, false
	}
	terminations := []model.TerminationMetadata{response.Termination}
	for _, choice := range response.Choices {
		terminations = append(terminations, terminationForChoice(choice))
	}
	for _, termination := range terminations {
		switch termination.Cause {
		case model.TerminationCauseContentFilter, model.TerminationCausePromptBlocked, model.TerminationCauseRecitation:
			if termination.Detail == "" {
				termination.Detail = "content_filter: " + termination.ProviderReason
			}
			return termination, true
		}
	}
	return model.TerminationMetadata{}, false
}

type answerNoticeBuffer struct {
	text     string
	payloads [][]byte
	bytes    int
	released bool
	disabled bool
}

func (buffer *answerNoticeBuffer) observe(text string, bypass bool) bool {
	if buffer.disabled {
		return false
	}
	if bypass || len(buffer.text)+len(text) > answerNoticeLimit {
		buffer.disabled = true
		buffer.text = ""
		return false
	}
	buffer.text += text
	trimmed := strings.TrimSpace(buffer.text)
	possible := trimmed == "" || strings.HasPrefix(googleNoticeStart, trimmed) || strings.HasPrefix(trimmed, googleNoticeStart)
	if !possible {
		buffer.disabled = true
		buffer.text = ""
	}
	return possible && trimmed != "" && !buffer.released
}

func (buffer *answerNoticeBuffer) blocked() bool {
	return !buffer.disabled && isExplicitAnswerNotice(buffer.text)
}
