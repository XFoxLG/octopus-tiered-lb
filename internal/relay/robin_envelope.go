package relay

import (
	"encoding/json"
	"io"
	"slices"
	"strings"

	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
)

// RobinEnvelopeKind classifies an OpenAI Chat assistant message without
// changing it. Catiecli/Robin has been observed returning provider-internal
// tool names in place of ordinary assistant text.
type RobinEnvelopeKind string

const (
	RobinEnvelopeNormalContent      RobinEnvelopeKind = "normal_content"
	RobinEnvelopeDeclaredToolCall   RobinEnvelopeKind = "declared_tool_call"
	RobinEnvelopeUnknownToolCall    RobinEnvelopeKind = "unknown_tool_call"
	RobinEnvelopeInsufficientOutput RobinEnvelopeKind = "insufficient_output"
	RobinEnvelopeMalformedOutput    RobinEnvelopeKind = "malformed_output"
)

// RobinEnvelopeReason gives callers a stable, bounded reason without exposing
// arbitrary provider arguments in diagnostics.
type RobinEnvelopeReason string

const (
	RobinReasonNormalContent                  RobinEnvelopeReason = "normal_content"
	RobinReasonEmptyResponse                  RobinEnvelopeReason = "empty_response"
	RobinReasonDeclaredFunctionCall           RobinEnvelopeReason = "declared_function_call"
	RobinReasonUnexpectedToolCall             RobinEnvelopeReason = "unexpected_tool_call"
	RobinReasonMultipleToolCalls              RobinEnvelopeReason = "multiple_tool_calls"
	RobinReasonNonFunctionToolCall            RobinEnvelopeReason = "non_function_tool_call"
	RobinReasonActualContentText              RobinEnvelopeReason = "actual_content_text"
	RobinReasonActualContentEmpty             RobinEnvelopeReason = "actual_content_empty"
	RobinReasonActualContentMalformed         RobinEnvelopeReason = "actual_content_malformed"
	RobinReasonActualContentUnexpectedContent RobinEnvelopeReason = "actual_content_unexpected_content"
	RobinReasonDispatchPayloadEmpty           RobinEnvelopeReason = "dispatch_stream_payload_empty"
	RobinReasonDispatchPayloadMalformed       RobinEnvelopeReason = "dispatch_stream_payload_malformed"
	RobinReasonMalformedMessage               RobinEnvelopeReason = "malformed_message"
)

// RobinEnvelopeClassification is observational. Text is populated only for a
// narrowly validated actual_content envelope; callers must require
// TextExtractable before considering any compatibility rewrite.
type RobinEnvelopeClassification struct {
	Kind            RobinEnvelopeKind
	Reason          RobinEnvelopeReason
	ToolName        string
	Text            string
	TextExtractable bool
}

const robinDispatchPrefix = "dispatch_stream_payload_"

// ClassifyRobinEnvelope inspects a single assistant message. A name declared by
// the client is always treated as a legitimate tool call. Unknown tools are
// never converted from their arguments into ordinary text.
func ClassifyRobinEnvelope(message *transmodel.Message, declaredToolNames []string) RobinEnvelopeClassification {
	if message == nil {
		return RobinEnvelopeClassification{
			Kind:   RobinEnvelopeMalformedOutput,
			Reason: RobinReasonMalformedMessage,
		}
	}

	if len(message.ToolCalls) == 0 {
		if robinMessageHasContent(message) {
			return RobinEnvelopeClassification{
				Kind:   RobinEnvelopeNormalContent,
				Reason: RobinReasonNormalContent,
			}
		}
		return RobinEnvelopeClassification{
			Kind:   RobinEnvelopeInsufficientOutput,
			Reason: RobinReasonEmptyResponse,
		}
	}

	for _, toolCall := range message.ToolCalls {
		if toolCall.Function.Name != "" && slices.Contains(declaredToolNames, toolCall.Function.Name) {
			return RobinEnvelopeClassification{
				Kind:     RobinEnvelopeDeclaredToolCall,
				Reason:   RobinReasonDeclaredFunctionCall,
				ToolName: toolCall.Function.Name,
			}
		}
	}

	if len(message.ToolCalls) != 1 {
		return RobinEnvelopeClassification{
			Kind:   RobinEnvelopeUnknownToolCall,
			Reason: RobinReasonMultipleToolCalls,
		}
	}

	toolCall := message.ToolCalls[0]
	name := strings.TrimSpace(toolCall.Function.Name)
	result := RobinEnvelopeClassification{
		Kind:     RobinEnvelopeUnknownToolCall,
		Reason:   RobinReasonUnexpectedToolCall,
		ToolName: name,
	}
	if !strings.EqualFold(strings.TrimSpace(toolCall.Type), "function") {
		result.Reason = RobinReasonNonFunctionToolCall
		return result
	}

	switch {
	case name == "actual_content":
		if message.Content.Content != nil || len(message.Content.MultipleContent) > 0 {
			result.Reason = RobinReasonActualContentUnexpectedContent
			return result
		}
		text, status := parseRobinActualContent(toolCall.Function.Arguments)
		switch status {
		case robinActualContentValid:
			result.Reason = RobinReasonActualContentText
			result.Text = text
			result.TextExtractable = true
		case robinActualContentEmpty:
			result.Reason = RobinReasonActualContentEmpty
		default:
			result.Reason = RobinReasonActualContentMalformed
		}
		return result
	case strings.HasPrefix(name, robinDispatchPrefix):
		result.Kind = RobinEnvelopeInsufficientOutput
		result.Reason = RobinReasonDispatchPayloadEmpty
		if !isRobinDispatchName(name) || !isEmptyRobinArguments(toolCall.Function.Arguments) {
			result.Reason = RobinReasonDispatchPayloadMalformed
		}
		return result
	default:
		return result
	}
}

func robinMessageHasContent(message *transmodel.Message) bool {
	if message.Content.Content != nil && strings.TrimSpace(*message.Content.Content) != "" {
		return true
	}
	if len(message.Content.MultipleContent) > 0 || len(message.Images) > 0 ||
		message.Audio != nil || strings.TrimSpace(message.Refusal) != "" {
		return true
	}
	return false
}

type robinActualContentStatus uint8

const (
	robinActualContentMalformed robinActualContentStatus = iota
	robinActualContentEmpty
	robinActualContentValid
)

func parseRobinActualContent(arguments string) (string, robinActualContentStatus) {
	if strings.TrimSpace(arguments) == "" {
		return "", robinActualContentEmpty
	}

	decoder := json.NewDecoder(strings.NewReader(arguments))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return "", robinActualContentMalformed
	}
	var content json.RawMessage
	seenContent := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return "", robinActualContentMalformed
		}
		key, ok := keyToken.(string)
		if !ok || key != "content" || seenContent {
			return "", robinActualContentMalformed
		}
		if err := decoder.Decode(&content); err != nil {
			return "", robinActualContentMalformed
		}
		seenContent = true
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return "", robinActualContentMalformed
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", robinActualContentMalformed
	}
	if !seenContent {
		return "", robinActualContentEmpty
	}
	if strings.TrimSpace(string(content)) == "null" {
		return "", robinActualContentEmpty
	}
	var text string
	if err := json.Unmarshal(content, &text); err != nil {
		return "", robinActualContentMalformed
	}
	if strings.TrimSpace(text) == "" {
		return "", robinActualContentEmpty
	}
	return text, robinActualContentValid
}

func isRobinDispatchName(name string) bool {
	if !strings.HasPrefix(name, robinDispatchPrefix) {
		return false
	}
	suffix := strings.TrimPrefix(name, robinDispatchPrefix)
	if len(suffix) != 16 {
		return false
	}
	for i := 0; i < len(suffix); i++ {
		if (suffix[i] < '0' || suffix[i] > '9') && (suffix[i] < 'a' || suffix[i] > 'f') {
			return false
		}
	}
	return true
}

func isEmptyRobinArguments(arguments string) bool {
	trimmed := strings.TrimSpace(arguments)
	return trimmed == "" || trimmed == "{}"
}
