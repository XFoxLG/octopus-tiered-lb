package relay

import (
	"io"
	"regexp"
	"strings"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

const answerNoticeLimit = 4 << 10
const googleNoticeStart = "The prompt could not be submitted."
const safetyRefusalStart = "I cannot fulfill this request."

type prefixResponseBody struct {
	io.Reader
	io.Closer
}

var (
	googleNoticePattern = regexp.MustCompile("^The prompt could not be submitted\\. The prompt contains sensitive words that violate Google's (?:Generative AI Prohibited Use policy|\\[Generative AI Prohibited Use policy\\]\\(https://policies\\.google\\.com/terms/generative-ai/use-policy\\))\\. Try rephrasing the prompt\\.(?: If you think this was an error, (?:send feedback|\\[send feedback\\]\\(https://ai\\.google\\.dev/gemini-api/docs/troubleshooting\\))\\.)?$")

	// These are complete, provider-generated safety refusals seen in production,
	// not generic "I can't help" sentences. Matching the full notification avoids
	// classifying a normal answer that merely discusses moderation policy.
	safetyRefusalPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^I cannot fulfill this request\. The prompt asks for the generation of sexually explicit fiction depicting graphic sexual acts and erotica\. My safety policies strictly prohibit generating highly explicit sexual content, even within creative writing, fictional roleplay, or user-provided narrative frameworks\.$`),
		regexp.MustCompile(`^I cannot fulfill this request\. The prompt asks for the continuation of a sexually explicit narrative involving graphic physical and sexual descriptions\. Under safety guidelines, I am unable to generate highly explicit sexual content or erotica, even within fictional creative writing or roleplay contexts\.$`),
		regexp.MustCompile(`^我无法协助生成高度露骨的色情描写及显式性行为细节。根据安全准则，涉及详细性器官描写及成人色情互动的内容均受到限制。如果您希望继续推进故事的主线剧情、角色对话或非露骨的情节发展，可以在合规范围内提出调整方向。$`),
	}
)

func isExplicitAnswerNotice(text string) bool {
	if len(text) > answerNoticeLimit {
		return false
	}
	text = strings.ReplaceAll(strings.TrimSpace(text), "’", "'")
	return googleNoticePattern.MatchString(text) || matchesAnySafetyRefusal(text)
}

func matchesAnySafetyRefusal(text string) bool {
	for _, pattern := range safetyRefusalPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
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
	possible := trimmed == "" ||
		strings.HasPrefix(googleNoticeStart, trimmed) ||
		strings.HasPrefix(trimmed, googleNoticeStart) ||
		strings.HasPrefix(safetyRefusalStart, trimmed) ||
		strings.HasPrefix(trimmed, safetyRefusalStart) ||
		strings.HasPrefix("我无法", trimmed)
	if !possible {
		buffer.disabled = true
		buffer.text = ""
	}
	return possible && trimmed != "" && !buffer.released
}

func (buffer *answerNoticeBuffer) blocked() bool {
	return !buffer.disabled && isExplicitAnswerNotice(buffer.text)
}
