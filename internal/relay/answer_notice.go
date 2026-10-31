package relay

import (
	"io"
	"strings"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

const answerNoticeLimit = 4 << 10

type prefixResponseBody struct {
	io.Reader
	io.Closer
}

func isExplicitAnswerNotice(text string) bool {
	// HTTP 200 body text is not provider termination metadata. A greeting,
	// generic error, refusal, or quoted moderation notice is ordinary answer
	// content. Only native termination fields are classified by
	// responseContentBlock below.
	_ = text
	return false
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
	_ = text
	_ = bypass
	return false
}

func (buffer *answerNoticeBuffer) blocked() bool {
	return !buffer.disabled && isExplicitAnswerNotice(buffer.text)
}
