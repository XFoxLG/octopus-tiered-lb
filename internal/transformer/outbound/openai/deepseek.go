package openai

import (
	"github.com/lingyuins/octopus/internal/transformer"
	"strings"

	"github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/utils/log"
)

func isDeepSeekCompatRequest(baseURL string, request *model.InternalLLMRequest) bool {
	return isProviderReasoningCompatRequest(baseURL, request, false, "deepseek")
}

func isMimoCompatRequest(baseURL string, request *model.InternalLLMRequest) bool {
	return isProviderReasoningCompatRequest(baseURL, request, true, "mimo")
}

func isReasoningCompatRequest(baseURL string, request *model.InternalLLMRequest, isMimoChannel bool) bool {
	return isProviderReasoningCompatRequest(baseURL, request, isMimoChannel, "deepseek") ||
		isProviderReasoningCompatRequest(baseURL, request, isMimoChannel, "mimo")
}

func isProviderReasoningCompatRequest(baseURL string, request *model.InternalLLMRequest, isMimoChannel bool, provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return false
	}

	if provider == "mimo" && isMimoChannel {
		return true
	}

	// DeepSeek/MiMo thinking controls are wire-format features of the *upstream*,
	// not of the client-facing model alias. Matching only on model name causes
	// Octopus to inject reasoning_effort / thinking into strict OpenAI-compat
	// proxies (e.g. FuturePPO) that expose deepseek-* aliases but reject those
	// fields with HTTP 400.
	lowerBaseURL := strings.ToLower(strings.TrimSpace(baseURL))
	if lowerBaseURL != "" && strings.Contains(lowerBaseURL, provider) {
		return true
	}
	if request == nil {
		return false
	}

	if strings.EqualFold(
		strings.TrimSpace(request.TransformerMetadata[model.TransformerMetadataGroupEndpointType]),
		provider,
	) {
		return true
	}

	// MiMo keeps model-name fallback (xiaomi / mimo) because many MiMo gateways
	// omit "mimo" from the host while still speaking the MiMo thinking schema.
	// DeepSeek does not: require base URL or explicit endpoint_type=deepseek.
	if provider == "mimo" {
		lowerModelName := strings.ToLower(strings.TrimSpace(request.Model))
		return strings.Contains(lowerModelName, "mimo") || strings.Contains(lowerModelName, "xiaomi")
	}

	return false
}

func looksLikeDeepSeekModelName(modelName string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelName)), "deepseek")
}

func normalizeDeepSeekReasoningCompat(request *model.InternalLLMRequest, baseURL string, isMimoChannel bool) {
	if request == nil || !isReasoningCompatRequest(baseURL, request, isMimoChannel) {
		return
	}

	thinkingType, hasThinkingType := extractDeepSeekThinkingType(request.ExtraBody)
	normalizedEffort := normalizeDeepSeekReasoningEffort(request.ReasoningEffort)
	if isProviderReasoningCompatRequest(baseURL, request, isMimoChannel, "mimo") {
		normalizedEffort = normalizeMimoReasoningEffort(request.ReasoningEffort)
	}
	originalEffort := strings.ToLower(strings.TrimSpace(request.ReasoningEffort))

	if hasThinkingType && thinkingType == "disabled" {
		request.ReasoningEffort = ""
	} else {
		request.ReasoningEffort = normalizedEffort
	}

	switch {
	case hasThinkingType && (thinkingType == "enabled" || thinkingType == "disabled"):
		request.ExtraBody = mergeDeepSeekThinkingExtraBody(request.ExtraBody, thinkingType)
	case originalEffort == "none":
		request.ExtraBody = mergeDeepSeekThinkingExtraBody(request.ExtraBody, "disabled")
	case request.ReasoningEffort != "":
		request.ExtraBody = mergeDeepSeekThinkingExtraBody(request.ExtraBody, "enabled")
	}
}

func normalizeDeepSeekReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "none", "auto":
		return ""
	case "minimal", "low":
		return "low"
	case "medium", "high", "xhigh":
		return "high"
	case "max":
		return "max"
	default:
		return strings.TrimSpace(effort)
	}
}

// MiMo accepts low/high/max and treats xhigh as its strongest tier. Keep this
// table separate from DeepSeek: DeepSeek maps xhigh to high, while existing
// MiMo wire compatibility maps xhigh to max.
func normalizeMimoReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "none", "auto":
		return ""
	case "low", "medium", "high":
		return "high"
	case "xhigh", "max":
		return "max"
	default:
		return strings.TrimSpace(effort)
	}
}

func extractDeepSeekThinkingType(raw transformer.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}

	var payload map[string]any
	if err := transformer.Unmarshal(raw, &payload); err != nil {
		return "", false
	}

	thinkingValue, ok := payload["thinking"]
	if !ok {
		return "", false
	}

	thinking, ok := thinkingValue.(map[string]any)
	if !ok {
		return "", false
	}

	typeValue, ok := thinking["type"]
	if !ok {
		return "", false
	}

	typeString, ok := typeValue.(string)
	if !ok {
		return "", false
	}

	normalized := strings.ToLower(strings.TrimSpace(typeString))
	return normalized, normalized != ""
}

func mergeDeepSeekThinkingExtraBody(raw transformer.RawMessage, thinkingType string) transformer.RawMessage {
	payload := map[string]any{}
	if len(raw) > 0 {
		if err := transformer.Unmarshal(raw, &payload); err != nil {
			log.Warnf("failed to unmarshal deepseek extra body: %v", err)
		}
	}

	payload["thinking"] = map[string]any{
		"type": thinkingType,
	}

	merged, err := transformer.Marshal(payload)
	if err != nil {
		return raw
	}
	return merged
}
