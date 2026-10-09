package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	appmodel "github.com/lingyuins/octopus/internal/model"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// capabilityProbeTimeout 是单项能力探测的超时预算。比协议层略长，因为
// 工具调用与结构化输出都要等模型真的生成一轮内容。
const capabilityProbeTimeout = 25 * time.Second

// capabilityProbeMaxBody 是单次探测读回的响应体上限。探测只需要判断结构，
// 不需要完整正文；设上限避免上游返回超大响应时把内存吃满。
const capabilityProbeMaxBody = 64 * 1024

// capabilityProbeMaxDetail 是落库的脱敏回显片段上限。
const capabilityProbeMaxDetail = 512

// capabilityProbeSemaphore 限制并发探测数。公益站普遍对并发敏感，
// 串行执行比"快"更重要。
var capabilityProbeSemaphore = make(chan struct{}, 1)

// probeHTTPStatusVerdict 把上游 HTTP 状态码映射成判定结论。
//
// 关键区分：
//   - 404 / 405：上游明确表示"没有这个端点" → Unsupported（正常状态，不算故障）；
//   - 401 / 403：凭据或权限问题 → Fail（是真实结论，但通常归因于 Key 而非协议）；
//   - 429 / 5xx / 408：限流或上游临时故障 → Unknown（绝不写入配置）。
func probeHTTPStatusVerdict(statusCode int) appmodel.ProbeVerdict {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return appmodel.ProbeVerdictPass
	case statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed:
		return appmodel.ProbeVerdictUnsupported
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return appmodel.ProbeVerdictFail
	case statusCode == http.StatusTooManyRequests,
		statusCode == http.StatusRequestTimeout,
		statusCode == http.StatusBadGateway,
		statusCode == http.StatusServiceUnavailable,
		statusCode == http.StatusGatewayTimeout,
		statusCode >= 500:
		return appmodel.ProbeVerdictUnknown
	case statusCode >= 400 && statusCode < 500:
		// 其余 4xx：上游明确拒绝了这个请求。多为"该模型不支持该参数"，
		// 但也不能一概而论（可能是请求体格式不被接受），保守判 Fail 而非 Unsupported。
		return appmodel.ProbeVerdictFail
	default:
		return appmodel.ProbeVerdictUnknown
	}
}

// probeUnsupportedKeywordPatterns 是"上游明确说不支持"的关键词表（小写子串）。
// 命中即判 Unsupported 而不是 Fail —— 这类信息对用户是有价值的配置结论，
// 不该被淹没在"失败"里。
var probeUnsupportedKeywordPatterns = []string{
	"not supported",
	"unsupported",
	"does not support",
	"doesn't support",
	"not implemented",
	"unknown parameter",
	"unrecognized request argument",
	"invalid parameter: tools",
	"不支持",
	"未实现",
	"不兼容",
}

// probeAuthKeywordPatterns 是凭据/权限类关键词，命中即判 Fail（真实结论）。
var probeAuthKeywordPatterns = []string{
	"invalid api key",
	"api key not valid",
	"incorrect api key",
	"unauthorized",
	"authentication",
	"permission denied",
	"insufficient permission",
	"无效的",
	"密钥",
	"未授权",
}

// probeRateLimitKeywordPatterns 是限流类关键词，命中即判 Unknown。
var probeRateLimitKeywordPatterns = []string{
	"rate limit",
	"rate_limit",
	"too many requests",
	"quota exceeded",
	"insufficient quota",
	"requests per minute",
	"tokens per minute",
	"限流",
	"请求数限制",
	"配额",
}

// classifyProbeBody 在 HTTP 状态码之外，用响应体关键词进一步细分判定。
// 返回 ok=false 表示关键词没有给出更精确的结论，调用方应沿用状态码判定。
func classifyProbeBody(body string) (appmodel.ProbeVerdict, bool) {
	lower := strings.ToLower(body)
	if lower == "" {
		return "", false
	}
	for _, pattern := range probeRateLimitKeywordPatterns {
		if strings.Contains(lower, pattern) {
			return appmodel.ProbeVerdictUnknown, true
		}
	}
	for _, pattern := range probeAuthKeywordPatterns {
		if strings.Contains(lower, pattern) {
			return appmodel.ProbeVerdictFail, true
		}
	}
	for _, pattern := range probeUnsupportedKeywordPatterns {
		if strings.Contains(lower, pattern) {
			return appmodel.ProbeVerdictUnsupported, true
		}
	}
	return "", false
}

// redactProbeDetail 在落库前抹掉可能的密钥泄漏。
//
// 探测回显直接来自上游，可能包含请求头回显或调试信息。这里做三件事：
// 抹掉显式传入的密钥、抹掉形如 sk-/Bearer 的 token、截断到上限。
func redactProbeDetail(body string, secrets ...string) string {
	redacted := body
	for _, secret := range secrets {
		trimmed := strings.TrimSpace(secret)
		if len(trimmed) < 8 {
			continue
		}
		redacted = strings.ReplaceAll(redacted, trimmed, "[redacted]")
	}
	redacted = redactBearerTokens(redacted)
	redacted = strings.TrimSpace(redacted)
	if len(redacted) > capabilityProbeMaxDetail {
		redacted = redacted[:capabilityProbeMaxDetail] + "…"
	}
	return redacted
}

// redactBearerTokens 抹掉文本里形如 "sk-xxxxx" 的密钥片段。
func redactBearerTokens(text string) string {
	fields := strings.Fields(text)
	for index, field := range fields {
		candidate := strings.Trim(field, `"',;:()[]{}`)
		if strings.HasPrefix(candidate, "sk-") && len(candidate) >= 12 {
			fields[index] = strings.Replace(field, candidate, "[redacted-key]", 1)
		}
	}
	return strings.Join(fields, " ")
}

// probeRequestOutcome 是单次 HTTP 探测的统一结果。
type probeRequestOutcome struct {
	StatusCode int
	Body       string
	Verdict    appmodel.ProbeVerdict
	LatencyMS  int64
	Summary    string
}

// sendProbeRequest 用给定 adapter 发一次真实探测请求，并把结果归一成判定结论。
//
// adapterType 只用于挑选 base URL（多协议渠道可为不同协议绑不同地址）。
// 返回的 outcome 一定非 nil；调用方据此决定是否写入配置。
func sendProbeRequest(
	ctx context.Context,
	channel *appmodel.Channel,
	key string,
	adapterType outbound.OutboundType,
	request *transmodel.InternalLLMRequest,
	capability ...string,
) *probeRequestOutcome {
	outcome := &probeRequestOutcome{Verdict: appmodel.ProbeVerdictUnknown}
	if channel == nil {
		outcome.Summary = "channel is nil"
		return outcome
	}
	plan, planErr := channel.ConnectionPlanFor(adapterType)
	if planErr != nil {
		outcome.Summary = planErr.Error()
		return outcome
	}
	adapter := plan.Adapter()
	if adapter == nil {
		outcome.Summary = fmt.Sprintf("unsupported adapter type: %d", adapterType)
		return outcome
	}

	probeCtx, cancel := context.WithTimeout(ctx, capabilityProbeTimeout)
	defer cancel()

	request, err := prepareConnectionProbeRequest(probeCtx, channel, plan, request, key)
	if err != nil {
		outcome.Summary = err.Error()
		return outcome
	}
	httpRequest, err := channel.BuildConnectionRequest(probeCtx, plan, adapter, request, strings.TrimSpace(key))
	if err != nil {
		outcome.Summary = fmt.Sprintf("failed to build probe request: %v", err)
		return outcome
	}
	if len(capability) > 0 && capability[0] == appmodel.ProbeItemWebSearch {
		if adapterType != outbound.OutboundTypeOpenAIResponse && adapterType != outbound.OutboundTypeGemini {
			outcome.Verdict = appmodel.ProbeVerdictUnsupported
			outcome.Summary = "No native search probe for this protocol"
			return outcome
		}
		var body map[string]any
		if err := json.NewDecoder(httpRequest.Body).Decode(&body); err != nil {
			outcome.Summary = "Could not build native search probe"
			return outcome
		}
		_ = httpRequest.Body.Close()
		if adapterType == outbound.OutboundTypeGemini {
			body["tools"] = []any{map[string]any{"google_search": map[string]any{}}}
		} else {
			body["tools"] = []any{map[string]any{"type": "web_search"}}
			body["tool_choice"] = map[string]any{"type": "web_search"}
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			outcome.Summary = "Could not encode native search probe"
			return outcome
		}
		httpRequest.Body = io.NopCloser(bytes.NewReader(encoded))
		httpRequest.ContentLength = int64(len(encoded))
		httpRequest.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(encoded)), nil }
	}
	channel.ApplyConnectionHeaders(httpRequest, plan, strings.TrimSpace(key))

	httpClient, err := ChannelHttpClient(channel)
	if err != nil {
		outcome.Summary = fmt.Sprintf("failed to build http client: %v", err)
		return outcome
	}

	startedAt := time.Now()
	response, err := httpClient.Do(httpRequest)
	outcome.LatencyMS = time.Since(startedAt).Milliseconds()
	if err != nil {
		// 网络错误/超时：证据不足，绝不据此改写配置。
		outcome.Verdict = appmodel.ProbeVerdictUnknown
		outcome.Summary = truncateProbeSummary(err.Error())
		return outcome
	}
	defer response.Body.Close()

	rawBody, readErr := io.ReadAll(io.LimitReader(response.Body, capabilityProbeMaxBody+1))
	outcome.StatusCode = response.StatusCode
	outcome.Body = string(rawBody)
	if readErr != nil || len(rawBody) > capabilityProbeMaxBody {
		outcome.Summary = "Incomplete or oversized probe response"
		return outcome
	}
	outcome.Verdict = probeHTTPStatusVerdict(response.StatusCode)
	outcome.Summary = response.Status
	if outcome.Verdict == appmodel.ProbeVerdictUnknown {
		return outcome
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if bodyVerdict, ok := classifyProbeBody(outcome.Body); ok {
			outcome.Verdict = bodyVerdict
		}
		return outcome
	}
	decodedResponse := *response
	decodedResponse.Body = io.NopCloser(bytes.NewReader(rawBody))
	decoded, decodeErr := adapter.TransformResponse(probeCtx, &decodedResponse)
	if decodeErr != nil || decoded == nil || decoded.Error != nil || (len(decoded.Choices) == 0 && len(decoded.EmbeddingData) == 0) {
		outcome.Verdict = appmodel.ProbeVerdictUnknown
		outcome.Summary = "Upstream did not return a valid generation response"
	}
	return outcome
}

func truncateProbeSummary(text string) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > capabilityProbeMaxDetail {
		return trimmed[:capabilityProbeMaxDetail] + "…"
	}
	return trimmed
}

// ===== 协议层探测 =====

// protocolProbeTarget 描述"用什么 adapter、发什么请求"去验证一条协议。
type protocolProbeTarget struct {
	Item        string
	Protocol    appmodel.UpstreamProtocol
	AdapterType outbound.OutboundType
	Request     *transmodel.InternalLLMRequest
}

// buildProtocolProbeTargets 构造协议层的探测目标列表。
//
// 对原生协议渠道（Gemini / Anthropic / 火山等）只探测它自己的原生协议：
// 拿一个 OpenAI 形状的请求去打原生端点永远是失败的，探了也没有信息量。
func buildProtocolProbeTargets(channelType outbound.OutboundType, modelName string) []protocolProbeTarget {
	stream := false
	chatRequest := func() *transmodel.InternalLLMRequest {
		return &transmodel.InternalLLMRequest{
			Model:        modelName,
			RawAPIFormat: transmodel.APIFormatOpenAIChatCompletion,
			Messages: []transmodel.Message{{
				Role:    "user",
				Content: transmodel.MessageContent{Content: stringPtr("hi")},
			}},
			Stream: &stream,
		}
	}
	responsesRequest := func() *transmodel.InternalLLMRequest {
		return &transmodel.InternalLLMRequest{
			Model:        modelName,
			RawAPIFormat: transmodel.APIFormatOpenAIResponse,
			Messages: []transmodel.Message{{
				Role:    "user",
				Content: transmodel.MessageContent{Content: stringPtr("hi")},
			}},
			Stream: &stream,
		}
	}
	messagesRequest := func() *transmodel.InternalLLMRequest {
		return &transmodel.InternalLLMRequest{
			Model:        modelName,
			RawAPIFormat: transmodel.APIFormatAnthropicMessage,
			Messages: []transmodel.Message{{
				Role:    "user",
				Content: transmodel.MessageContent{Content: stringPtr("hi")},
			}},
			Stream: &stream,
		}
	}

	switch channelType {
	case outbound.OutboundTypeGemini:
		return []protocolProbeTarget{{
			Item:        appmodel.ProbeItemProtocolGemini,
			Protocol:    appmodel.UpstreamProtocolMessages,
			AdapterType: outbound.OutboundTypeGemini,
			Request:     chatRequest(),
		}}
	case outbound.OutboundTypeAnthropic:
		return []protocolProbeTarget{{
			Item:        appmodel.ProbeItemProtocolMessages,
			Protocol:    appmodel.UpstreamProtocolMessagesOnly,
			AdapterType: outbound.OutboundTypeAnthropic,
			Request:     messagesRequest(),
		}}
	case outbound.OutboundTypeVolcengine:
		return []protocolProbeTarget{{
			Item:        appmodel.ProbeItemProtocolResponses,
			Protocol:    appmodel.UpstreamProtocolResponsesOnly,
			AdapterType: outbound.OutboundTypeVolcengine,
			Request:     responsesRequest(),
		}}
	case outbound.OutboundTypeCloudflare:
		return []protocolProbeTarget{{
			Item:        appmodel.ProbeItemProtocolChat,
			Protocol:    appmodel.UpstreamProtocolChatOnly,
			AdapterType: outbound.OutboundTypeCloudflare,
			Request:     chatRequest(),
		}}
	default:
		// OpenAI 兼容渠道：三种协议各试一次，让用户看到"这个站到底吃哪几种"。
		return []protocolProbeTarget{
			{
				Item:        appmodel.ProbeItemProtocolChat,
				Protocol:    appmodel.UpstreamProtocolChatOnly,
				AdapterType: outbound.OutboundTypeOpenAIChat,
				Request:     chatRequest(),
			},
			{
				Item:        appmodel.ProbeItemProtocolResponses,
				Protocol:    appmodel.UpstreamProtocolResponsesOnly,
				AdapterType: outbound.OutboundTypeOpenAIResponse,
				Request:     responsesRequest(),
			},
			{
				Item:        appmodel.ProbeItemProtocolMessages,
				Protocol:    appmodel.UpstreamProtocolMessagesOnly,
				AdapterType: outbound.OutboundTypeAnthropic,
				Request:     messagesRequest(),
			},
		}
	}
}

// ===== 能力层探测 =====

// buildCapabilityProbeRequest 构造一项能力探测的请求体。
//
// 各项的判据与参考实现（all-api-hub 的 5 项探测）对齐：
//   - text_generation 要求模型回 "OK"；
//   - tool_calling 用 required 逼出一次工具调用；
//   - structured_output 要求返回符合 JSON Schema 的对象 {"ok":true}；
//   - web_search 让模型走一次联网检索；
//   - models 走 /models 列表，不需要真实生成。
func buildCapabilityProbeRequest(item, modelName string) (*transmodel.InternalLLMRequest, error) {
	stream := false
	switch item {
	case appmodel.ProbeItemTextGeneration:
		return &transmodel.InternalLLMRequest{
			Model:        modelName,
			RawAPIFormat: transmodel.APIFormatOpenAIChatCompletion,
			Messages: []transmodel.Message{{
				Role:    "user",
				Content: transmodel.MessageContent{Content: stringPtr("Reply with exactly: OK")},
			}},
			Stream: &stream,
		}, nil
	case appmodel.ProbeItemToolCalling:
		required := "required"
		return &transmodel.InternalLLMRequest{
			Model:        modelName,
			RawAPIFormat: transmodel.APIFormatOpenAIChatCompletion,
			Messages: []transmodel.Message{{
				Role:    "user",
				Content: transmodel.MessageContent{Content: stringPtr("Call the verify_tool tool once.")},
			}},
			Tools: []transmodel.Tool{{
				Type: "function",
				Function: transmodel.Function{
					Name:        "verify_tool",
					Description: "Return a timestamp string.",
					Parameters:  []byte(`{"type":"object","properties":{}}`),
				},
			}},
			ToolChoice: &transmodel.ToolChoice{ToolChoice: &required},
			Stream:     &stream,
		}, nil
	case appmodel.ProbeItemStructuredOutput:
		strict := true
		responseFormat := &transmodel.ResponseFormat{
			Type: "json_schema",
			JSONSchema: &transmodel.ResponseFormatJSONSchema{
				Name:   "verify_schema",
				Strict: &strict,
				Schema: []byte(`{"type":"object","properties":{"ok":{"type":"boolean","const":true}},"required":["ok"],"additionalProperties":false}`),
			},
		}
		return &transmodel.InternalLLMRequest{
			Model:          modelName,
			RawAPIFormat:   transmodel.APIFormatOpenAIChatCompletion,
			Messages:       []transmodel.Message{{Role: "user", Content: transmodel.MessageContent{Content: stringPtr(`Return a JSON object with shape { "ok": true }.`)}}},
			ResponseFormat: responseFormat,
			Stream:         &stream,
		}, nil
	case appmodel.ProbeItemWebSearch:
		// 联网搜索没有跨厂商统一的请求形状：OpenAI 侧是内建 web_search 工具，
		// Google 侧是 grounding。这里用"要求模型检索一条近期新闻"作为通用探针，
		// 由上游自行决定是否启用其检索能力；判据是响应里出现可识别的检索痕迹
		// 或非空正文（见 judgeCapabilityResponse）。
		return &transmodel.InternalLLMRequest{
			Model:        modelName,
			RawAPIFormat: transmodel.APIFormatOpenAIChatCompletion,
			Messages: []transmodel.Message{{
				Role:    "user",
				Content: transmodel.MessageContent{Content: stringPtr("Search the web and tell me one recent AI headline.")},
			}},
			Stream: &stream,
		}, nil
	default:
		return nil, fmt.Errorf("unknown capability probe item: %s", item)
	}
}

// capabilityResponseSignals 是从响应体里提取的判定信号。
type capabilityResponseSignals struct {
	HasToolCall     bool
	HasStructuredOK bool
	HasSearchTrace  bool
	HasVisibleText  bool
	RefusalDetected bool
}

// collectCapabilitySignals 解析上游响应体，提取各类能力信号。
//
// 解析刻意宽松：不同上游对同一能力的字段命名差异很大（OpenAI 的 tool_calls、
// Anthropic 的 tool_use、Gemini 的 functionCall 等）。这里只看"有没有出现
// 对应结构"，不做严格校验——严格校验会把兼容实现误判为不支持。
func collectCapabilitySignals(rawBody string) capabilityResponseSignals {
	signals := capabilityResponseSignals{}
	if strings.TrimSpace(rawBody) == "" {
		return signals
	}
	var decoded any
	if err := json.Unmarshal([]byte(rawBody), &decoded); err != nil {
		// 非 JSON 响应：只能判断"有正文"。
		signals.HasVisibleText = strings.TrimSpace(rawBody) != ""
		return signals
	}
	walkCapabilitySignals(decoded, &signals)
	return signals
}

func walkCapabilitySignals(node any, signals *capabilityResponseSignals) {
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			switch strings.ToLower(key) {
			case "tool_calls", "tool_use", "functioncall", "function_call":
				if !isEmptyJSONValue(value) {
					signals.HasToolCall = true
				}
			case "type":
				// Anthropic 的工具调用是 "type": "tool_use" 而不是独立的 tool_use 键；
				// Gemini 部分实现同理。只认键名会漏掉这些协议。
				if marker, ok := value.(string); ok {
					switch strings.ToLower(strings.TrimSpace(marker)) {
					case "tool_use", "function_call", "tool_call":
						signals.HasToolCall = true
					case "web_search_call", "google_search_call", "grounding":
						signals.HasSearchTrace = true
					}
				}
			case "citations", "groundingmetadata", "annotations", "web_search_call", "search_results":
				if !isEmptyJSONValue(value) {
					signals.HasSearchTrace = true
				}
			case "refusal":
				if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
					signals.RefusalDetected = true
				}
			case "content", "text", "output_text", "completion":
				if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
					signals.HasVisibleText = true
					var object map[string]any
					if json.Unmarshal([]byte(text), &object) == nil && len(object) == 1 && object["ok"] == true {
						signals.HasStructuredOK = true
					}
				}
			case "ok":
				if boolean, ok := value.(bool); ok && boolean {
					signals.HasStructuredOK = true
				}
			}
			walkCapabilitySignals(value, signals)
		}
	case []any:
		for _, item := range typed {
			walkCapabilitySignals(item, signals)
		}
	}
}

func isEmptyJSONValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	case string:
		return strings.TrimSpace(typed) == ""
	default:
		return false
	}
}

// judgeCapabilityResponse 把能力项 + HTTP 结果 + 响应信号归一成最终判定。
//
// 这一层只处理"HTTP 通过了但内容不合格"的情况：HTTP 未通过时沿用状态码判定，
// 因为那时响应体里通常没有可信的能力信息。
func judgeCapabilityResponse(item string, outcome *probeRequestOutcome) appmodel.ProbeVerdict {
	if outcome == nil {
		return appmodel.ProbeVerdictUnknown
	}
	if outcome.Verdict != appmodel.ProbeVerdictPass {
		return outcome.Verdict
	}
	signals := collectCapabilitySignals(outcome.Body)
	switch item {
	case appmodel.ProbeItemTextGeneration:
		// 只要拿到可见正文就算通过；具体文案不强制（不同上游会加前后缀、
		// 或把 "OK" 包在 markdown 里，强制相等会把可用渠道误判为失败）。
		if signals.HasVisibleText {
			return appmodel.ProbeVerdictPass
		}
		return appmodel.ProbeVerdictFail
	case appmodel.ProbeItemToolCalling:
		if signals.HasToolCall {
			return appmodel.ProbeVerdictPass
		}
		if signals.RefusalDetected {
			return appmodel.ProbeVerdictUnsupported
		}
		return appmodel.ProbeVerdictFail
	case appmodel.ProbeItemStructuredOutput:
		if signals.HasStructuredOK {
			return appmodel.ProbeVerdictPass
		}
		return appmodel.ProbeVerdictFail
	case appmodel.ProbeItemWebSearch:
		if signals.HasSearchTrace {
			return appmodel.ProbeVerdictPass
		}
		// 没有检索痕迹但有正文：模型可能只是"凭记忆回答"。
		// 这种证据不足以判定支持，也不足以判定不支持 → Unknown（不写入）。
		return appmodel.ProbeVerdictUnknown
	default:
		return outcome.Verdict
	}
}

// buildCapabilityProbeAdapter 选出承载某一项能力探测的 adapter。
// 能力项一律走各协议中最"通用"的那个：OpenAI 兼容渠道用 Chat Completions，
// 原生渠道用它自己的原生 adapter。
func buildCapabilityProbeAdapter(channelType outbound.OutboundType) outbound.OutboundType {
	switch channelType {
	case outbound.OutboundTypeAnthropic:
		return outbound.OutboundTypeAnthropic
	case outbound.OutboundTypeGemini:
		return outbound.OutboundTypeGemini
	case outbound.OutboundTypeVolcengine:
		return outbound.OutboundTypeVolcengine
	case outbound.OutboundTypeCloudflare:
		return outbound.OutboundTypeCloudflare
	default:
		return outbound.OutboundTypeOpenAIChat
	}
}

// capabilityProbeSupportedForChannel 报告某项能力探测对该渠道类型是否有意义。
// 例如联网搜索对 Anthropic 原生渠道没有统一入口，直接标 Unsupported 比
// 发一个注定失败的请求更诚实也更省额度。
func capabilityProbeSupportedForChannel(item string, channelType outbound.OutboundType) bool {
	if !outbound.IsChatChannelType(channelType) {
		return false
	}
	if item == appmodel.ProbeItemWebSearch && channelType == outbound.OutboundTypeAnthropic {
		return false
	}
	return true
}
