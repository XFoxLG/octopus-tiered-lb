package outbound

import (
	"strings"

	"github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound/anthropic"
	"github.com/lingyuins/octopus/internal/transformer/outbound/cloudflare"
	"github.com/lingyuins/octopus/internal/transformer/outbound/codex"
	"github.com/lingyuins/octopus/internal/transformer/outbound/gemini"
	"github.com/lingyuins/octopus/internal/transformer/outbound/mimo"
	"github.com/lingyuins/octopus/internal/transformer/outbound/openai"
	"github.com/lingyuins/octopus/internal/transformer/outbound/passthrough"
	"github.com/lingyuins/octopus/internal/transformer/outbound/volcengine"
)

type OutboundType int

const (
	OutboundTypeOpenAIChat OutboundType = iota
	OutboundTypeOpenAIResponse
	OutboundTypeAnthropic
	OutboundTypeGemini
	OutboundTypeVolcengine
	OutboundTypeOpenAIEmbedding
	OutboundTypeMimo
	OutboundTypeCloudflare
	OutboundTypePassthrough
	OutboundTypeCodex
	// OutboundTypeRaw 原始穿透（信息体）：保留客户端原始请求体与请求路径，
	// 仅改写 model 字段。仅由分组出站格式 "raw" 触发，不是渠道类型。
	OutboundTypeRaw
)

func (t OutboundType) String() string {
	switch t {
	case OutboundTypeOpenAIChat:
		return "chat"
	case OutboundTypeOpenAIResponse:
		return "response"
	case OutboundTypeAnthropic:
		return "anthropic"
	case OutboundTypeGemini:
		return "gemini"
	case OutboundTypeVolcengine:
		return "volcengine"
	case OutboundTypeOpenAIEmbedding:
		return "embedding"
	case OutboundTypeMimo:
		return "mimo"
	case OutboundTypeCloudflare:
		return "cloudflare"
	case OutboundTypePassthrough:
		return "passthrough"
	case OutboundTypeCodex:
		return "codex"
	case OutboundTypeRaw:
		return "raw"
	default:
		return "unknown"
	}
}

// EmbeddingChannelTypes 定义支持 embedding 请求的 channel 类型集合
var EmbeddingChannelTypes = map[OutboundType]bool{
	OutboundTypeOpenAIEmbedding: true,
}

// ChatChannelTypes 定义支持 chat 请求的 channel 类型集合
var ChatChannelTypes = map[OutboundType]bool{
	OutboundTypeOpenAIChat:     true,
	OutboundTypeOpenAIResponse: true,
	OutboundTypeAnthropic:      true,
	OutboundTypeGemini:         true,
	OutboundTypeVolcengine:     true,
	OutboundTypeMimo:           true,
	OutboundTypeCloudflare:     true,
	OutboundTypeCodex:          true,
}

// IsEmbeddingChannelType 判断 channel 类型是否支持 embedding 请求
func IsEmbeddingChannelType(channelType OutboundType) bool {
	return EmbeddingChannelTypes[channelType]
}

// IsChatChannelType 判断 channel 类型是否支持 chat 请求
func IsChatChannelType(channelType OutboundType) bool {
	return ChatChannelTypes[channelType]
}

var outboundFactories = map[OutboundType]func() model.Outbound{
	OutboundTypeOpenAIChat:      func() model.Outbound { return &openai.ChatOutbound{} },
	OutboundTypeOpenAIResponse:  func() model.Outbound { return &openai.ResponseOutbound{} },
	OutboundTypeOpenAIEmbedding: func() model.Outbound { return &openai.EmbeddingOutbound{} },
	OutboundTypeAnthropic:       func() model.Outbound { return &anthropic.MessageOutbound{} },
	OutboundTypeGemini:          func() model.Outbound { return &gemini.MessagesOutbound{} },
	OutboundTypeVolcengine:      func() model.Outbound { return &volcengine.ResponseOutbound{} },
	OutboundTypeMimo:            func() model.Outbound { return &mimo.ChatOutbound{} },
	OutboundTypeCloudflare:      func() model.Outbound { return &cloudflare.ChatOutbound{} },
	OutboundTypePassthrough:     func() model.Outbound { return &passthrough.Outbound{} },
	OutboundTypeCodex:           func() model.Outbound { return &codex.Outbound{} },
	OutboundTypeRaw:             func() model.Outbound { return &passthrough.Outbound{PreservePath: true} },
}

func Get(outboundType OutboundType) model.Outbound {
	if factory, ok := outboundFactories[outboundType]; ok {
		return factory()
	}
	return nil
}

// IsLLMRequestFormat 判断请求是否为 LLM 对话格式（ChatCompletion / Response / Anthropic Message）。
// 从 relay 包提取以供 helper 包的探测逻辑复用，避免 helper 导入 relay 造成循环依赖。
func IsLLMRequestFormat(request *model.InternalLLMRequest) bool {
	if request == nil {
		return false
	}
	switch request.RawAPIFormat {
	case model.APIFormatOpenAIChatCompletion, model.APIFormatOpenAIResponse, model.APIFormatAnthropicMessage:
		return true
	default:
		return false
	}
}

// EndpointProtocolForAdapter 把出站 adapter 映射成 base URL 协议绑定用的协议名。
//
// 返回值与渠道 UpstreamProtocols / BaseUrl.Protocol 使用同一套词汇
// （chat / responses / messages）。多协议渠道可以给不同协议绑定不同地址，
// 调用方在选出本次要用的 adapter 后，用它来挑对应的地址。
//
// 返回空串表示该 adapter 没有可绑定的对话协议（透传、嵌入、未知类型），
// 此时地址选择应回落到默认条目，保证未绑定协议的渠道行为不变。
func EndpointProtocolForAdapter(adapterType OutboundType) string {
	switch adapterType {
	case OutboundTypeOpenAIChat:
		return "chat"
	case OutboundTypeOpenAIResponse:
		return "responses"
	case OutboundTypeAnthropic:
		return "messages"
	default:
		return ""
	}
}

// SupportsPassthroughFormat 判断给定渠道类型是否能用透传（passthrough/raw）
// 适配器承载该请求格式。
//
// 透传适配器只做两件事：把入站原始 JSON 体里的 model 字段改写掉、按入站协议
// 选择上游端点。它只认 Chat Completions / Responses / Anthropic Messages
// 三种入站格式（见 passthrough.delegateAndEndpointForFormat）。
//
// 透传对「OpenAI 兼容渠道」有意义：请求体原样送到上游，由上游自己解析。对
// 原生协议渠道（Gemini / 火山 / Cloudflare 等）则是有害的——它们的适配器负责
// 把 OpenAI 形状的请求体翻译成各自的原生形状，透传会跳过这层翻译，把一个
// OpenAI 形状的 body 发到原生端点上，请求必然失败（且会被上游当成畸形请求
// 计入渠道健康度）。所以这些渠道必须回落到自己的原生适配器。
//
// 注意 Cloudflare 虽然是 OpenAI 风格入站，但端点路径形如
// /ai/run/@cf/{model}（由 model 名拼出），透传的固定端点路径不成立，同样排除。
func SupportsPassthroughFormat(channelType OutboundType, request *model.InternalLLMRequest) bool {
	if request == nil {
		return false
	}
	switch channelType {
	case OutboundTypeOpenAIChat, OutboundTypeOpenAIResponse, OutboundTypeMimo:
		return IsLLMRequestFormat(request)
	default:
		return false
	}
}

// ResolveAttemptTypesForChannel 根据 channel type、请求格式决定出站 adapter 尝试
// 顺序，并在分组 outbound_format 之上叠加渠道级协议声明。
//
// 渠道声明的解析优先级（高→低）：
//  1. channelProtocols（Channel.UpstreamProtocols，多协议有序列表）；
//  2. channelOverride（Channel.OutboundFormatOverride，仅 chat_only /
//     responses_only 的旧字段，保留以兼容未迁移配置）；
//  3. groupOutboundFormat。
//
// 前两层都为空时逐字节等价于改动前行为，存量部署升级当天零行为变化。
//
// 对 OpenAIChat / OpenAIResponse 类型，提供可配置的 adapter 回退优先级：
//   - "passthrough": 原样转发 inbound JSON body，禁用 adapter 回退。
//   - "raw": 与 passthrough 相同，但额外保留客户端原始请求路径。
//   - "chat" / 默认(auto): 优先 Chat Completions，回退 Responses API。
//   - "responses": 优先 Responses API，回退 Chat Completions。
//   - "messages": 优先 Anthropic Messages，回退 Chat → Responses。
//   - "chat_only" / "responses_only" / "messages_only": 禁用跨格式回退。
//
// 其它 channel type 直接返回 [channelType]。
func ResolveAttemptTypesForChannel(channelType OutboundType, request *model.InternalLLMRequest, groupOutboundFormat, channelOverride string) []OutboundType {
	switch override := strings.ToLower(strings.TrimSpace(channelOverride)); override {
	case "chat_only", "responses_only":
		return resolveAttemptTypesByFormat(channelType, request, override)
	default:
		return resolveAttemptTypesByFormat(channelType, request, groupOutboundFormat)
	}
}

// ResolveAttemptTypesForChannelDeclared 是 ResolveAttemptTypesForChannel 的多协议
// 版本：渠道可以声明多个协议及其优先级，本函数把它们展开成 adapter 尝试序列。
//
// 展开规则（对 OpenAI 兼容渠道）：
//   - 先收集所有「非透传」协议的各自首选项，按声明顺序拼接并去重；
//   - 声明里只有严格模式（chat_only 等）时序列长度为 1，天然禁用回退；
//   - 声明里出现 passthrough/raw 时直接短路返回单个透传 adapter；
//   - 声明为空时回落到 channelOverride → groupOutboundFormat。
//
// 这样「只勾 Chat Completions」= 只试 Chat，「勾 Chat + Responses」= 先 Chat 再
// Responses，「勾 Responses + Chat」= 先 Responses 再 Chat，与用户在前端的
// 勾选顺序一致。
func ResolveAttemptTypesForChannelDeclared(channelType OutboundType, request *model.InternalLLMRequest, groupOutboundFormat, channelOverride string, channelProtocols []string) []OutboundType {
	if len(channelProtocols) == 0 {
		return ResolveAttemptTypesForChannel(channelType, request, groupOutboundFormat, channelOverride)
	}

	normalized := make([]string, 0, len(channelProtocols))
	for _, raw := range channelProtocols {
		protocol := strings.ToLower(strings.TrimSpace(raw))
		if protocol == "" {
			continue
		}
		normalized = append(normalized, protocol)
	}
	if len(normalized) == 0 {
		return ResolveAttemptTypesForChannel(channelType, request, groupOutboundFormat, channelOverride)
	}

	// 透传是「整体转发」，一旦声明就独占，不需要也不应该拼接其它协议。
	// 非透传渠道声明透传时按渠道类型闸门回落，避免打坏原生协议渠道。
	for _, protocol := range normalized {
		if protocol != "passthrough" && protocol != "raw" {
			continue
		}
		if SupportsPassthroughFormat(channelType, request) {
			return resolveAttemptTypesByFormat(channelType, request, protocol)
		}
		return resolveAttemptTypesByFormat(channelType, request, "")
	}

	if request == nil || !IsLLMRequestFormat(request) {
		return resolveAttemptTypesByFormat(channelType, request, "")
	}
	if channelType != OutboundTypeOpenAIChat && channelType != OutboundTypeOpenAIResponse && channelType != OutboundTypeMimo {
		// 原生协议渠道没有「协议回退」概念：它只会说自己的原生协议。
		return resolveAttemptTypesByFormat(channelType, request, "")
	}

	attemptTypes := make([]OutboundType, 0, len(normalized)+1)
	appended := make(map[OutboundType]bool, len(normalized)+1)
	appendType := func(outboundType OutboundType) {
		if appended[outboundType] {
			return
		}
		appended[outboundType] = true
		attemptTypes = append(attemptTypes, outboundType)
	}
	// strictOnly 记录是否所有声明都是严格模式。严格模式下允许跨协议拼接
	// （用户显式勾了两个严格协议就是要依次试），但单个严格协议不再补回退项。
	for _, protocol := range normalized {
		for _, outboundType := range resolveAttemptTypesByFormat(channelType, request, protocol) {
			appendType(outboundType)
		}
	}
	if len(attemptTypes) == 0 {
		return resolveAttemptTypesByFormat(channelType, request, "")
	}
	return attemptTypes
}

func resolveAttemptTypesByFormat(channelType OutboundType, request *model.InternalLLMRequest, outboundFormat string) []OutboundType {
	format := strings.ToLower(strings.TrimSpace(outboundFormat))
	// 透传必须先过渠道类型闸门：分组级的 passthrough/raw 会应用到分组内所有渠道，
	// 但原生协议渠道（Gemini / 火山 / Cloudflare 等）无法被透传承载。放宽这一步
	// 会让这些渠道被塞进透传适配器、把 OpenAI 形状的请求体发到原生端点，属于
	// 主动打坏渠道的缺陷。不支持的渠道回落到自己的原生适配器。
	if SupportsPassthroughFormat(channelType, request) {
		switch format {
		case "passthrough":
			return []OutboundType{OutboundTypePassthrough}
		case "raw":
			return []OutboundType{OutboundTypeRaw}
		}
	}
	if request != nil && IsLLMRequestFormat(request) && (channelType == OutboundTypeOpenAIChat || channelType == OutboundTypeOpenAIResponse) {
		switch format {
		case "responses":
			return []OutboundType{OutboundTypeOpenAIResponse, OutboundTypeOpenAIChat}
		case "messages":
			return []OutboundType{OutboundTypeAnthropic, OutboundTypeOpenAIChat, OutboundTypeOpenAIResponse}
		case "chat_only":
			return []OutboundType{OutboundTypeOpenAIChat}
		case "responses_only":
			return []OutboundType{OutboundTypeOpenAIResponse}
		case "messages_only":
			return []OutboundType{OutboundTypeAnthropic}
		default: // auto / chat
			return []OutboundType{OutboundTypeOpenAIChat, OutboundTypeOpenAIResponse}
		}
	}
	return []OutboundType{channelType}
}
