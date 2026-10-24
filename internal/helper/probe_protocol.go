package helper

import "github.com/lingyuins/octopus/internal/transformer/outbound"

func probeProtocolForAdapter(adapter outbound.OutboundType) string {
	switch adapter {
	case outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeMimo:
		return "chat"
	case outbound.OutboundTypeOpenAIResponse:
		return "responses"
	case outbound.OutboundTypeAnthropic:
		return "messages"
	case outbound.OutboundTypeGemini:
		return "gemini"
	case outbound.OutboundTypeOpenAIEmbedding:
		return "embeddings"
	case outbound.OutboundTypeCloudflare:
		return "cloudflare"
	case outbound.OutboundTypeVolcengine:
		return "volcengine"
	case outbound.OutboundTypeCodex:
		return "codex"
	default:
		return ""
	}
}
