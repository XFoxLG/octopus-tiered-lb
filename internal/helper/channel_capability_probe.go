package helper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

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
const capabilityProbeMaxDetail = capabilityProbeMaxBody

const capabilityProbeMaxSummary = 512

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
	return truncateProbeText(redacted, capabilityProbeMaxDetail)
}

// redactBearerTokens 抹掉文本里形如 "sk-xxxxx" 的密钥片段。
var probeBearerTokenPattern = regexp.MustCompile(`(?i)\bBearer[ \t]+[A-Za-z0-9._~+/-]+=*`)
var probeSKTokenPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{9,}`)

func redactBearerTokens(text string) string {
	text = probeBearerTokenPattern.ReplaceAllString(text, "Bearer [redacted-token]")
	return probeSKTokenPattern.ReplaceAllString(text, "[redacted-key]")
}

// truncateProbeText preserves UTF-8 and marks bounded diagnostic fragments.
func truncateProbeText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	end := limit - len("…")
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + "…"
}

// probeKeyScope 是本次探测使用的渠道 Key 的审计标识。
//
// 只保留下标与 Key ID 范围，**绝不携带 Key 本身**：落库的
// ChannelProbeResult.KeyScope 就是它的字符串形式。
// Index < 0 表示调用方不知道位置（例如临时构造的 Key）。
type probeKeyScope struct {
	Index int
	KeyID int
	NoKey bool
}

func (s probeKeyScope) String() string {
	if s.NoKey {
		return "none"
	}
	parts := make([]string, 0, 2)
	if s.Index >= 0 {
		parts = append(parts, fmt.Sprintf("key_index:%d", s.Index))
	}
	if s.KeyID > 0 {
		parts = append(parts, fmt.Sprintf("key_id:%d", s.KeyID))
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, ",")
}

// probeRequestOutcome 是单次 HTTP 探测的统一结果。
//
// 除判定结论外，它还携带「这次到底怎么发的」：实际协议、接口、去掉凭据的地址、
// Key 范围、stream/budget 与请求体指纹。这些字段直接落库（迁移 082），
// 让两次对照可以证明端点/协议/模型/Key/正文/stream/budget 一致。
type probeRequestOutcome struct {
	Protocol           string
	EndpointID         string
	EndpointURL        string
	KeyScope           string
	RequestStream      *bool
	RequestBudgetMS    *int64
	RequestFingerprint string
	StatusCode         int
	Body               string
	Verdict            appmodel.ProbeVerdict
	LatencyMS          int64
	Summary            string
}

// probeRequestFingerprintMaxBody 是指纹覆盖的正文上限。超过上限就不给指纹，
// 而不是拿一个被截断的 hash 冒充完整请求体（对照结论必须是可复现的）。
const probeRequestFingerprintMaxBody = 1 << 20

// probeFingerprint 计算方法 + 路径 + 正文的 SHA-256。刻意不含 URL query 与任何
// header：Gemini legacy 把 Key 放在 `?key=`，header 里也总有 Authorization。
func probeFingerprint(req *http.Request, body []byte) string {
	if req == nil {
		return ""
	}
	path := ""
	if req.URL != nil {
		path = req.URL.EscapedPath()
	}
	digest := sha256.New()
	digest.Write([]byte(req.Method))
	digest.Write([]byte{'\n'})
	digest.Write([]byte(path))
	digest.Write([]byte{'\n'})
	digest.Write(body)
	return hex.EncodeToString(digest.Sum(nil))
}

// captureProbeRequestBody 读出请求体用于指纹，再把正文原样放回，调用方仍能正常发送。
// 第二个返回值为 false 表示正文读取失败或超出指纹上限（此时不产生指纹）。
func captureProbeRequestBody(req *http.Request) ([]byte, bool) {
	if req == nil || req.Body == nil {
		return nil, true
	}
	original := req.Body
	head, err := io.ReadAll(io.LimitReader(original, probeRequestFingerprintMaxBody+1))
	if err != nil {
		// 读取中断：把已读到的部分与剩余流拼回去，绝不丢字节。
		req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(head), original))
		req.ContentLength = -1
		req.GetBody = nil
		return nil, false
	}
	if len(head) > probeRequestFingerprintMaxBody {
		req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(head), original))
		return nil, false
	}
	req.Body = io.NopCloser(bytes.NewReader(head))
	req.ContentLength = int64(len(head))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(head)), nil }
	return head, true
}

// auditProbeEndpointURL 把实际请求地址压成可落库的形式：去掉 userinfo 与 query
// （凭据可能藏在两者里），并按当前 Key 再做一次脱敏兜底。
func auditProbeEndpointURL(u *url.URL, key string) string {
	if u == nil {
		return ""
	}
	sanitized := *u
	sanitized.User = nil
	sanitized.RawQuery = ""
	sanitized.ForceQuery = false
	sanitized.Fragment = ""
	sanitized.RawFragment = ""
	return truncateProbeText(redactProbeDetail(sanitized.String(), key), 512)
}

// sendProbeRequest 用给定 adapter 发一次真实探测请求，并把结果归一成判定结论。
//
// adapterType 只用于挑选 base URL（多协议渠道可为不同协议绑不同地址）。
// 返回的 outcome 一定非 nil；调用方据此决定是否写入配置。
func sendProbeRequest(
	ctx context.Context,
	channel *appmodel.Channel,
	key string,
	scope probeKeyScope,
	adapterType outbound.OutboundType,
	request *transmodel.InternalLLMRequest,
	capability ...string,
) *probeRequestOutcome {
	outcome := &probeRequestOutcome{Verdict: appmodel.ProbeVerdictUnknown, KeyScope: scope.String()}
	budgetMS := capabilityProbeTimeout.Milliseconds()
	outcome.RequestBudgetMS = &budgetMS
	if request != nil && request.Stream != nil {
		stream := *request.Stream
		outcome.RequestStream = &stream
	}
	if channel == nil {
		outcome.Summary = "channel is nil"
		return outcome
	}
	plan, planErr := channel.ConnectionPlanFor(adapterType)
	if planErr != nil {
		outcome.Summary = planErr.Error()
		return outcome
	}
	if plan.Endpoint != nil {
		outcome.Protocol = plan.Endpoint.Protocol
		outcome.EndpointID = plan.Endpoint.ID
	} else {
		outcome.Protocol = probeProtocolForAdapter(plan.AdapterType)
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
			outcome.Verdict = appmodel.ProbeVerdictUnknown
			outcome.Summary = "No native search probe is implemented for this protocol; upstream support was not tested"
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
	// 记录「这次实际打向哪里、发的什么正文」——地址与指纹都不含凭据。
	outcome.EndpointURL = auditProbeEndpointURL(httpRequest.URL, key)
	if body, ok := captureProbeRequestBody(httpRequest); ok {
		outcome.RequestFingerprint = probeFingerprint(httpRequest, body)
	}

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
	return truncateProbeText(trimmed, capabilityProbeMaxSummary)
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
		// 出站时补原生搜索工具，判据是响应里出现可识别的检索证据；
		// 普通正文不算搜索通过（见 judgeCapabilityResponse）。
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

// collectCapabilitySignals reads answer envelopes, never metadata or echoed requests.
// A tool probe must call the requested verify_tool with parseable arguments.
// Structured output must be the answer itself, with no Markdown fence, extra
// property, or surrounding prose.
func collectCapabilitySignals(rawBody string) capabilityResponseSignals {
	signals := capabilityResponseSignals{}
	var root map[string]any
	if json.Unmarshal([]byte(rawBody), &root) != nil {
		return signals
	}
	if len(root) == 1 && root["ok"] == true {
		signals.HasStructuredOK = true
	}
	collectProbeEnvelope(root, &signals)
	return signals
}

func collectProbeEnvelope(root map[string]any, signals *capabilityResponseSignals) {
	if choices, ok := root["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				collectProbeAnswer(message, signals)
				collectProbeTools(message["tool_calls"], signals)
				if call, ok := message["function_call"].(map[string]any); ok {
					collectProbeTool(call, signals)
				}
			}
		}
	}
	if output, ok := root["output"].([]any); ok {
		for _, entry := range output {
			if item, ok := entry.(map[string]any); ok {
				switch item["type"] {
				case "message":
					collectProbeAnswer(item, signals)
				case "function_call":
					collectProbeTool(item, signals)
				case "web_search_call":
					signals.HasSearchTrace = true
				}
			}
		}
	}
	if _, ok := root["content"]; ok {
		collectProbeAnswer(root, signals)
	}
	if candidates, ok := root["candidates"].([]any); ok && len(candidates) > 0 {
		if candidate, ok := candidates[0].(map[string]any); ok {
			if content, ok := candidate["content"].(map[string]any); ok {
				collectProbeAnswer(map[string]any{"content": content["parts"]}, signals)
			}
			signals.HasSearchTrace = signals.HasSearchTrace || !isEmptyJSONValue(candidate["groundingMetadata"])
			if reason, ok := candidate["finishReason"].(string); ok && (reason == "SAFETY" || reason == "PROHIBITED_CONTENT" || reason == "BLOCKLIST") {
				signals.RefusalDetected = true
			}
		}
	}
	if feedback, ok := root["promptFeedback"].(map[string]any); ok && !isEmptyJSONValue(feedback["blockReason"]) {
		signals.RefusalDetected = true
	}
	if root["stop_reason"] == "refusal" {
		signals.RefusalDetected = true
	}
	signals.HasSearchTrace = signals.HasSearchTrace || !isEmptyJSONValue(root["citations"])
	if text, ok := root["output_text"].(string); ok {
		collectProbeText(text, signals)
	}
	if result, ok := root["result"].(map[string]any); ok {
		if text, ok := result["response"].(string); ok {
			collectProbeText(text, signals)
		}
		if _, ok := result["choices"]; ok {
			collectProbeEnvelope(result, signals)
		}
	}
}

func collectProbeAnswer(message map[string]any, signals *capabilityResponseSignals) {
	if refusal, ok := message["refusal"].(string); ok && strings.TrimSpace(refusal) != "" {
		signals.RefusalDetected = true
	}
	if text, ok := message["content"].(string); ok {
		collectProbeText(text, signals)
		return
	}
	parts, _ := message["content"].([]any)
	var text strings.Builder
	for _, value := range parts {
		part, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if part["type"] == "refusal" {
			signals.RefusalDetected = true
		}
		if refusal, ok := part["refusal"].(string); ok && strings.TrimSpace(refusal) != "" {
			signals.RefusalDetected = true
		}
		// Thinking blocks are not the generated answer.
		if part["thought"] == true {
			continue
		}
		if part["type"] == nil || part["type"] == "text" || part["type"] == "output_text" {
			if value, ok := part["text"].(string); ok {
				text.WriteString(value)
			}
		}
		if part["type"] == "tool_use" {
			collectProbeTool(part, signals)
		}
		if call, ok := part["functionCall"].(map[string]any); ok {
			collectProbeTool(call, signals)
		}
		if !isEmptyJSONValue(part["annotations"]) {
			signals.HasSearchTrace = true
		}
	}
	collectProbeText(text.String(), signals)
}

func collectProbeText(text string, signals *capabilityResponseSignals) {
	if strings.TrimSpace(text) == "" {
		return
	}
	signals.HasVisibleText = true
	var object map[string]any
	if json.Unmarshal([]byte(text), &object) == nil && len(object) == 1 && object["ok"] == true {
		signals.HasStructuredOK = true
	}
}

func collectProbeTools(value any, signals *capabilityResponseSignals) {
	calls, _ := value.([]any)
	for _, value := range calls {
		call, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if function, ok := call["function"].(map[string]any); ok {
			collectProbeTool(function, signals)
		}
	}
}

// collectProbeTool 只在工具名与参数都成立时记为一次有效工具调用。
//
// 名称必须是探测要求的那一个；参数必须是可解析的 JSON 对象。各家字段名不同
// （OpenAI 用 arguments，Anthropic 用 input，Gemini 用 args）。探测 schema 没有
// 任何必填属性，所以空对象（或空字符串）算符合；参数缺失、不是对象、解析失败
// 都不构成「工具调用可用」的证据。
func collectProbeTool(call map[string]any, signals *capabilityResponseSignals) {
	if call["name"] != "verify_tool" {
		return
	}
	if _, ok := probeToolArguments(call); !ok {
		return
	}
	signals.HasToolCall = true
}

func probeToolArguments(call map[string]any) (map[string]any, bool) {
	value, ok := call["arguments"]
	if !ok {
		value, ok = call["input"]
	}
	if !ok {
		value, ok = call["args"]
	}
	if !ok {
		return nil, false
	}
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case string:
		if strings.TrimSpace(typed) == "" {
			// 无参工具被序列化成空字符串：等价于空对象，仍符合探测 schema。
			return map[string]any{}, true
		}
		var object map[string]any
		if json.Unmarshal([]byte(typed), &object) != nil || object == nil {
			return nil, false
		}
		return object, true
	default:
		return nil, false
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
		if outcome.StatusCode >= 400 && strings.TrimSpace(outcome.Body) != "" {
			outcome.Summary = outcome.Summary + ": " + outcome.Body
		}
		return outcome.Verdict
	}
	signals := collectCapabilitySignals(outcome.Body)
	if signals.RefusalDetected {
		outcome.Summary = "Upstream refused this probe; capability support remains unverified"
		return appmodel.ProbeVerdictUnknown
	}
	switch item {
	case appmodel.ProbeItemTextGeneration:
		// 只要拿到可见正文就算通过；具体文案不强制（不同上游会加前后缀、
		// 或把 "OK" 包在 markdown 里，强制相等会把可用渠道误判为失败）。
		if signals.HasVisibleText {
			outcome.Summary = "Received visible answer text"
			return appmodel.ProbeVerdictPass
		}
		outcome.Summary = "No visible answer text was returned"
		return appmodel.ProbeVerdictFail
	case appmodel.ProbeItemToolCalling:
		if signals.HasToolCall {
			outcome.Summary = "Received a verify_tool call"
			return appmodel.ProbeVerdictPass
		}
		outcome.Summary = "verify_tool was not called; this probe did not verify tool support"
		return appmodel.ProbeVerdictFail
	case appmodel.ProbeItemStructuredOutput:
		if signals.HasStructuredOK {
			outcome.Summary = "Answer matches the requested JSON object {ok:true}"
			return appmodel.ProbeVerdictPass
		}
		outcome.Summary = "Expected a plain JSON object {ok:true}; Markdown fences, extra fields, or prose do not satisfy this probe"
		return appmodel.ProbeVerdictFail
	case appmodel.ProbeItemWebSearch:
		if signals.HasSearchTrace {
			outcome.Summary = "Received native search evidence"
			return appmodel.ProbeVerdictPass
		}
		outcome.Summary = "No native search evidence was returned; search support remains unverified"
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
// 没有实现原生探针时只记录 Unknown，不把探针缺失写成上游不支持。
func capabilityProbeSupportedForChannel(item string, channelType outbound.OutboundType) bool {
	if !outbound.IsChatChannelType(channelType) {
		return false
	}
	if item == appmodel.ProbeItemWebSearch && channelType == outbound.OutboundTypeAnthropic {
		return false
	}
	return true
}
