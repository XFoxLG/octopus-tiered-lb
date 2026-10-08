package relay

import (
	"strings"

	dbmodel "github.com/lingyuins/octopus/internal/model"
	ch "github.com/lingyuins/octopus/internal/op/channel"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/log"
)

// itemSupportsEndpoint checks whether a group item likely supports the
// target endpoint type. This is used to filter * group items before
// the balancer, preventing chat-only items from being tried against
// media endpoints like image_generation or music_generation.
func itemSupportsEndpoint(item dbmodel.GroupItem, channel dbmodel.Channel, endpointType string) bool {
	if channel.ConnectionConfig != nil {
		return channelSupportsEndpoint(channel, endpointType)
	}
	return channelSupportsEndpoint(channel, endpointType) || modelNameHintsEndpoint(item.ModelName, endpointType)
}

// channelSupportsEndpoint checks whether a channel's type hints at support
// for the given endpoint type.
//
// Currently this is a thin layer: the outbound type system maps
// OutboundTypeOpenAIEmbedding → embeddings, and all chat types
// are considered "might support anything." This is intentionally
// conservative — a chat-type channel might support image/music/video
// via an OpenAI-compatible upstream.
func channelSupportsEndpoint(channel dbmodel.Channel, endpointType string) bool {
	switch endpointType {
	case dbmodel.EndpointTypeEmbeddings:
		if channel.ConnectionConfig != nil {
			for _, e := range channel.ConnectionConfig.Endpoints {
				if e.Protocol == "embeddings" {
					return true
				}
			}
			return false
		}
		return outbound.IsEmbeddingChannelType(channel.Type)
	default:
		return false
	}
}

// modelNameHintsEndpoint checks whether a model name contains keywords
// that suggest support for a specific endpoint type.
//
// This is a heuristic layer — it cannot guarantee 100% accuracy, but
// for * group scenarios it's sufficient to avoid blindly trying
// chat-only models against image/music/video endpoints.
func modelNameHintsEndpoint(modelName string, endpointType string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	if lower == "" {
		return false
	}

	switch endpointType {
	case dbmodel.EndpointTypeEmbeddings:
		return strings.Contains(lower, "embedding") ||
			strings.Contains(lower, "bge") ||
			strings.Contains(lower, "gte") ||
			strings.Contains(lower, "e5") ||
			strings.Contains(lower, "jina-clip")
	case dbmodel.EndpointTypeRerank:
		return strings.Contains(lower, "rerank") ||
			strings.Contains(lower, "re-rank") ||
			strings.Contains(lower, "colbert") ||
			strings.Contains(lower, "cohere-rerank")
	case dbmodel.EndpointTypeModerations:
		return strings.Contains(lower, "moderation") ||
			strings.Contains(lower, "moderat") ||
			strings.Contains(lower, "omni-moderation")
	case dbmodel.EndpointTypeImageGeneration:
		return strings.Contains(lower, "image") ||
			strings.Contains(lower, "flux") ||
			strings.Contains(lower, "stable-diffusion") ||
			strings.Contains(lower, "sd3") ||
			strings.Contains(lower, "sd-") ||
			strings.Contains(lower, "gpt-image") ||
			strings.Contains(lower, "dall-e") ||
			strings.Contains(lower, "dalle") ||
			strings.Contains(lower, "qwen-image") ||
			strings.Contains(lower, "imagen") ||
			strings.Contains(lower, "seedream") ||
			strings.Contains(lower, "agnes-image") ||
			strings.Contains(lower, "midjourney") ||
			strings.Contains(lower, "ideogram") ||
			strings.Contains(lower, "playground")
	case dbmodel.EndpointTypeAudioSpeech:
		return strings.Contains(lower, "tts") ||
			strings.Contains(lower, "speech") ||
			strings.Contains(lower, "voice") ||
			strings.Contains(lower, "audio-speech") ||
			strings.Contains(lower, "playht") ||
			strings.Contains(lower, "elevenlabs") ||
			strings.Contains(lower, "cartesia")
	case dbmodel.EndpointTypeAudioTranscription:
		return strings.Contains(lower, "whisper") ||
			strings.Contains(lower, "transcription") ||
			strings.Contains(lower, "transcribe") ||
			strings.Contains(lower, "audio-transcri") ||
			strings.Contains(lower, "deepgram")
	case dbmodel.EndpointTypeVideoGeneration:
		return strings.Contains(lower, "video") ||
			strings.Contains(lower, "veo") ||
			strings.Contains(lower, "kling") ||
			strings.Contains(lower, "wan") ||
			strings.Contains(lower, "sora") ||
			strings.Contains(lower, "agnes") ||
			strings.Contains(lower, "luma") ||
			strings.Contains(lower, "runway") ||
			strings.Contains(lower, "animate") ||
			strings.Contains(lower, "svd") ||
			strings.Contains(lower, "seendance") ||
			strings.Contains(lower, "vidu") ||
			strings.Contains(lower, "paiwo") ||
			strings.Contains(lower, "hailuo")
	case dbmodel.EndpointTypeMusicGeneration:
		return strings.Contains(lower, "music") ||
			strings.Contains(lower, "suno") ||
			strings.Contains(lower, "udio") ||
			strings.Contains(lower, "stable-audio") ||
			strings.Contains(lower, "audio-craft")
	case dbmodel.EndpointTypeSearch:
		return strings.Contains(lower, "search") ||
			strings.Contains(lower, "serper") ||
			strings.Contains(lower, "brave-search") ||
			strings.Contains(lower, "exa") ||
			strings.Contains(lower, "tavily") ||
			strings.Contains(lower, "sonar") ||
			strings.Contains(lower, "deepsearch") ||
			strings.Contains(lower, "deep-research") ||
			strings.Contains(lower, "deepresearch")
	default:
		return false
	}
}

// narrowGroupItemsForEndpoint filters a group's items to only those
// whose channel/model name hints at support for the given endpoint type.
//
// The returned group is a shallow copy with the filtered Items slice.
// If no items match, the returned group has Items == nil.
func narrowGroupItemsForEndpoint(group dbmodel.Group, endpointType string) dbmodel.Group {
	var kept []dbmodel.GroupItem
	for _, item := range group.Items {
		ch, err := ch.Get(item.ChannelID, nil)
		if err != nil {
			log.Warnf("narrowGroupItems: failed to get channel %d: %v", item.ChannelID, err)
			continue
		}
		if itemSupportsEndpoint(item, *ch, endpointType) {
			kept = append(kept, item)
		}
	}
	group.Items = kept
	return group
}
