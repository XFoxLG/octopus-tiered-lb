package helper

import "encoding/json"

// ParseRerankTokenUsage accepts explicit nonnegative input/output counters only.
// Total-only or conflicting values cannot identify trustworthy token usage.
func ParseRerankTokenUsage(body []byte) (inputTokens, outputTokens int, reported bool) {
	var response struct {
		Error json.RawMessage `json:"error"`
		Usage *struct {
			Prompt     *int `json:"prompt_tokens"`
			Input      *int `json:"input_tokens"`
			Completion *int `json:"completion_tokens"`
			Output     *int `json:"output_tokens"`
			Total      *int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil || response.Usage == nil || (len(response.Error) > 0 && string(response.Error) != "null") {
		return 0, 0, false
	}
	u := response.Usage
	if (u.Prompt != nil && u.Input != nil && *u.Prompt != *u.Input) || (u.Completion != nil && u.Output != nil && *u.Completion != *u.Output) {
		return 0, 0, false
	}
	input, output := u.Prompt, u.Completion
	if input == nil {
		input = u.Input
	}
	if output == nil {
		output = u.Output
	}
	if input == nil || *input < 0 || (output != nil && *output < 0) {
		return 0, 0, false
	}
	inputTokens = *input
	if output != nil {
		outputTokens = *output
	}
	if u.Total != nil && (*u.Total < inputTokens || *u.Total-inputTokens != outputTokens) {
		return 0, 0, false
	}
	return inputTokens, outputTokens, true
}
