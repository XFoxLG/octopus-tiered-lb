package model

import (
	"bytes"
	"encoding/json"
	"math"
)

// ParseNonChatTokenUsage reads the token envelopes used by OpenAI-compatible
// embeddings/rerank and SiliconFlow rerank. Result counts and billed search
// units are not tokens. A missing direction is never inferred from totals.
func ParseNonChatTokenUsage(body []byte) *Usage {
	type counters struct {
		Prompt     *int64 `json:"prompt_tokens"`
		Input      *int64 `json:"input_tokens"`
		Completion *int64 `json:"completion_tokens"`
		Output     *int64 `json:"output_tokens"`
		Total      *int64 `json:"total_tokens"`
	}
	var envelope struct {
		Error  json.RawMessage `json:"error"`
		Usage  *counters       `json:"usage"`
		Tokens *counters       `json:"tokens"`
	}
	if json.Unmarshal(body, &envelope) != nil || (len(envelope.Error) > 0 && !bytes.Equal(bytes.TrimSpace(envelope.Error), []byte("null"))) {
		return nil
	}
	var input, output, total *int64
	merge := func(target **int64, value *int64) bool {
		if value == nil {
			return true
		}
		if *value < 0 || (*target != nil && **target != *value) {
			return false
		}
		*target = value
		return true
	}
	for _, block := range []*counters{envelope.Usage, envelope.Tokens} {
		if block == nil {
			continue
		}
		if !merge(&input, block.Prompt) || !merge(&input, block.Input) ||
			!merge(&output, block.Completion) || !merge(&output, block.Output) || !merge(&total, block.Total) {
			return nil
		}
	}
	if input == nil {
		return nil
	}
	outputReported := output != nil
	usage := &Usage{PromptTokens: *input, NonChatOutputReported: &outputReported}
	if output != nil {
		usage.CompletionTokens = *output
	}
	if total != nil {
		if *total < usage.PromptTokens || *total-usage.PromptTokens != usage.CompletionTokens {
			return nil
		}
		usage.TotalTokens = *total
	} else {
		if usage.CompletionTokens > math.MaxInt64-usage.PromptTokens {
			return nil
		}
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage
}
