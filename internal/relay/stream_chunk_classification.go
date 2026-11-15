package relay

import "github.com/lingyuins/octopus/internal/transformer/model"

// Flags describe parsed content, not the absence of visible text.
func streamChunkStructure(resp *model.InternalLLMResponse) bool {
	if streamChunkReasoningBytes(resp) > 0 || streamChunkHasEffectiveOutput(resp) {
		return false
	}
	for _, choice := range resp.Choices {
		if choice.Delta != nil && (choice.Delta.Role != "" || len(choice.Delta.ToolCalls) > 0 || choice.Delta.ReasoningSignature != nil) {
			return true
		}
	}
	return false
}
func streamChunkIsEmptyControl(resp *model.InternalLLMResponse) bool {
	if resp == nil || resp.Usage != nil || resp.Error != nil || resp.SawTerminalEvent || resp.Termination.HasCause() || resp.Object == "[DONE]" {
		return false
	}
	if len(resp.Choices) == 0 {
		return true
	}
	for _, choice := range resp.Choices {
		if choice.FinishReason != nil || choice.Termination.HasCause() || choice.Logprobs != nil {
			return false
		}
		d := choice.Delta
		if d == nil || d.Role != "" || d.GetReasoningContent() != "" || d.ReasoningSignature != nil || d.Refusal != "" || len(d.ToolCalls) > 0 || len(d.Images) > 0 || d.Audio != nil || len(d.Annotations) > 0 || d.ErrorMessage != "" || len(d.Content.MultipleContent) > 0 || (d.Content.Content != nil && *d.Content.Content != "") {
			return false
		}
	}
	return true
}
func streamChunkHasEffectiveOutput(resp *model.InternalLLMResponse) bool {
	if resp == nil {
		return false
	}
	for _, c := range resp.Choices {
		d := c.Delta
		if d == nil {
			continue
		}
		if d.Content.Content != nil && *d.Content.Content != "" {
			return true
		}
		if len(d.Content.MultipleContent) > 0 || len(d.Images) > 0 || d.Audio != nil || d.Refusal != "" {
			return true
		}
		for _, tool := range d.ToolCalls {
			if tool.Function.Arguments != "" {
				return true
			}
		}
	}
	return false
}
