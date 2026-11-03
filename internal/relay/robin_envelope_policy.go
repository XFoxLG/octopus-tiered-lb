package relay

import (
	"strings"

	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// applyRobinEnvelopePolicyToResponse classifies a complete non-streaming Chat
// response before it is rendered to the client. Empty provider-internal
// dispatch envelopes are treated as no-output retries. Rewriting
// actual_content is opt-in and only applies to the classifier's strict shape.
func (ra *relayAttempt) applyRobinEnvelopePolicyToResponse(response *transmodel.InternalLLMResponse) error {
	return ra.applyRobinEnvelopePolicyToResponseWithExtraction(response, isRobinActualContentExtractEnabled())
}

func (ra *relayAttempt) applyRobinEnvelopePolicyToResponseWithExtraction(response *transmodel.InternalLLMResponse, extractActualContent bool) error {
	if ra == nil || response == nil || ra.adapterEndpointProtocol() != "chat" || ra.robinPolicyDisabledForRawForwarding() {
		return nil
	}
	// Explicit provider failures and refusals own the response outcome. Do not
	// turn an empty internal tool envelope into a retry before that terminal
	// meaning has been delivered.
	if responseHasProviderRefusal(response) {
		return nil
	}
	if _, providerFailure := responseProviderFailure(response); providerFailure {
		return nil
	}

	request := ra.robinPolicyRequest()
	declared := declaredRobinToolNames(request)
	insufficient := false
	hasUsableChoice := false
	for i := range response.Choices {
		choice := &response.Choices[i]
		if choice.Message == nil {
			continue
		}
		classification := ClassifyRobinEnvelope(choice.Message, declared)
		ra.recordRobinEnvelope(classification)
		if isRobinEmptyDispatchClassification(classification) {
			insufficient = true
			continue
		}
		if classification.Kind != RobinEnvelopeInsufficientOutput {
			hasUsableChoice = true
		}
		if extractActualContent && classification.TextExtractable &&
			!choiceHasProviderTerminal(*choice) {
			choice.Message.Content = transmodel.MessageContent{Content: robinStringPtr(classification.Text)}
			choice.Message.ToolCalls = nil
			choice.FinishReason = robinStringPtr("stop")
			choice.Termination = transmodel.TerminationMetadata{Cause: transmodel.TerminationCauseComplete}
		}
	}
	// A multi-choice response may legitimately contain one unusable internal
	// envelope and another valid completion. Retry only when every choice is an
	// unusable dispatch envelope; otherwise preserve the valid result and its
	// bounded diagnostic classification.
	if insufficient && !hasUsableChoice {
		return errRobinInsufficientOutput
	}
	return nil
}

// applyRobinEnvelopePolicyToStreamChunk handles only a complete terminal
// tool-call frame. Partial tool-name/argument fragments are left untouched so
// the proxy never mistakes an unterminated stream fragment for a full envelope.
func (ra *relayAttempt) applyRobinEnvelopePolicyToStreamChunk(chunk *transmodel.InternalLLMResponse) error {
	return ra.applyRobinEnvelopePolicyToStreamChunkWithExtraction(chunk, isRobinActualContentExtractEnabled())
}

func (ra *relayAttempt) applyRobinEnvelopePolicyToStreamChunkWithExtraction(chunk *transmodel.InternalLLMResponse, extractActualContent bool) error {
	if ra == nil || chunk == nil || ra.adapterEndpointProtocol() != "chat" || ra.robinPolicyDisabledForRawForwarding() {
		return nil
	}
	if responseHasProviderRefusal(chunk) {
		return nil
	}
	if _, providerFailure := responseProviderFailure(chunk); providerFailure {
		return nil
	}

	request := ra.robinPolicyRequest()
	declared := declaredRobinToolNames(request)
	insufficient := false
	hasUsableChoice := false
	for i := range chunk.Choices {
		choice := &chunk.Choices[i]
		if choice.Delta == nil || len(choice.Delta.ToolCalls) == 0 || !isToolCallTerminalChoice(*choice) {
			continue
		}
		if choiceHasProviderTerminal(*choice) {
			continue
		}
		classification := ClassifyRobinEnvelope(choice.Delta, declared)
		ra.recordRobinEnvelope(classification)
		if isRobinEmptyDispatchClassification(classification) {
			insufficient = true
			continue
		}
		if classification.Kind != RobinEnvelopeInsufficientOutput {
			hasUsableChoice = true
		}
		if extractActualContent && classification.TextExtractable {
			choice.Delta.Content = transmodel.MessageContent{Content: robinStringPtr(classification.Text)}
			choice.Delta.ToolCalls = nil
			choice.FinishReason = robinStringPtr("stop")
			choice.Termination = transmodel.TerminationMetadata{Cause: transmodel.TerminationCauseComplete}
		}
	}
	// With n > 1, a chunk may only contain one choice. Do not abort the whole
	// stream for a single empty envelope while another choice can still arrive.
	multiChoice := len(chunk.Choices) > 1 || requestedChatChoiceCount(ra.internalRequest) > 1
	if insufficient && !hasUsableChoice && !multiChoice && !ra.streamOutputWasCommitted() {
		return errRobinInsufficientOutput
	}
	return nil
}

func (ra *relayAttempt) robinPolicyRequest() *transmodel.InternalLLMRequest {
	if ra == nil {
		return nil
	}
	if ra.metrics != nil && ra.metrics.InternalRequest != nil {
		return ra.metrics.InternalRequest
	}
	return ra.internalRequest
}

func (ra *relayAttempt) robinPolicyDisabledForRawForwarding() bool {
	return ra != nil &&
		(ra.adapterType == outbound.OutboundTypeRaw || ra.adapterType == outbound.OutboundTypePassthrough)
}

func declaredRobinToolNames(request *transmodel.InternalLLMRequest) []string {
	if request == nil || len(request.Tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(request.Tools))
	for i := range request.Tools {
		name := strings.TrimSpace(request.Tools[i].Function.Name)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func isToolCallTerminalChoice(choice transmodel.Choice) bool {
	if choice.FinishReason != nil {
		switch strings.ToLower(strings.TrimSpace(*choice.FinishReason)) {
		case "tool_calls", "function_call":
			return true
		}
	}
	return choice.Termination.Cause == transmodel.TerminationCauseToolCall
}

func choiceHasProviderTerminal(choice transmodel.Choice) bool {
	cause := terminationForChoice(choice).Cause
	return cause.IsProviderFailure() || cause.IsProviderRefusal()
}

func requestedChatChoiceCount(request *transmodel.InternalLLMRequest) int {
	if request == nil || len(request.RawRequest) == 0 {
		return 1
	}
	var payload struct {
		N int `json:"n"`
	}
	if err := jsonAPI.Unmarshal(request.RawRequest, &payload); err != nil || payload.N <= 1 {
		return 1
	}
	return payload.N
}

func isRobinEmptyDispatchClassification(classification RobinEnvelopeClassification) bool {
	return classification.Kind == RobinEnvelopeInsufficientOutput &&
		classification.Reason == RobinReasonDispatchPayloadEmpty
}

func (ra *relayAttempt) recordRobinEnvelope(classification RobinEnvelopeClassification) {
	if ra == nil || ra.metrics == nil || ra.metrics.RequestTrace == nil {
		return
	}
	ra.metrics.RequestTrace.recordRobinEnvelope(ra.logAttemptNumber, classification)
}

func robinStringPtr(value string) *string {
	return &value
}
