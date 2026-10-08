package relay

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Match explicit diagnostics about this option, not generic "not supported"
// or an echoed request which happens to contain reasoning_effort. Patterns
// are compiled once and evaluated only on failed upstream HTTP responses.
var reasoningCompatibilityPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\breasoning_effort\b\s+is\s+(?:not enabled|not supported)\s+(?:for|with)\s+this model`),
	regexp.MustCompile(`(?is)\bunexpected reasoning effort\b.{0,96}\bsupported (?:types|values|levels)\b`),
	regexp.MustCompile(`(?i)\b(?:unsupported (?:parameter|value)|unrecognized request argument supplied):\s*["']?reasoning_effort\b`),
	// Pydantic errors are returned as either JSON or Python dict strings by
	// private gateways. Tie the validation message to this exact body field.
	regexp.MustCompile(`(?i)["']loc["']\s*:\s*[\[(]\s*["']body["']\s*,\s*["']reasoning_effort["']\s*[\])]\s*,\s*["']msg["']\s*:\s*["'](?:input should be|extra inputs are not permitted)`),
}

func isReasoningCompatibilityError(upstreamText string) bool {
	message := upstreamText
	if start := strings.IndexByte(upstreamText, '{'); start >= 0 {
		var envelope struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(upstreamText[start:]), &envelope) == nil {
			if envelope.Error != nil {
				message = envelope.Error.Message
			} else if envelope.Message != "" {
				message = envelope.Message
			}
		}
	}
	for _, pattern := range reasoningCompatibilityPatterns {
		if pattern.MatchString(message) {
			return true
		}
	}
	return false
}
