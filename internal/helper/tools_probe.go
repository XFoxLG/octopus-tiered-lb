package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	appmodel "github.com/lingyuins/octopus/internal/model"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// toolsProbeTimeout 单次探测请求的独立超时预算。
const toolsProbeTimeout = 12 * time.Second

// toolsProbeMaxBody 探测响应体的读取上限（只需判断 tool_call 存在性）。
const toolsProbeMaxBody = 8 * 1024

// toolsProbeSemaphore 限制并发探测数，避免批量探测打爆上游。
var toolsProbeSemaphore = make(chan struct{}, 1)

// toolsProbeUnsupportedPatterns 是「tools 不支持」类上游错误的白名单（小写子串匹配）。
var toolsProbeUnsupportedPatterns = []string{
	"tools not supported",
	"tools are not supported",
	"unsupported parameter: tools",
	"the tools parameter is not supported",
	"does not support the tools parameter",
	"unrecognized request argument supplied: tools",
	"does not support tools",
	"not support tools",
	"tool calls are not supported",
	"tool calls not supported",
	"does not support tool calls",
	"tool calling is not supported",
	"tool calling not supported",
	"does not support tool calling",
	"function calling not supported",
	"function calling is not supported",
	"does not support function calling",
	"不支持工具",
	"不支持函数调用",
	"不支持 tools",
	"工具调用不支持",
	"tools 参数不支持",
}

// toolsProbeConfirmCounts 是白名单「≥2 次确认」的进程内计数。
// key=(channelID, model)，value=命中次数；成功探测后清零。
var toolsProbeConfirmCounts = struct {
	sync.Mutex
	counts map[string]int
}{counts: make(map[string]int)}

// TestToolsSupport 探测渠道×模型是否支持 tools 调用（Seller 判别矩阵移植）。
// toolChoice: ""=auto；"required"=手动判别（含 4xx 降级 auto 对照）。
// 返回判别结果；error 表示完全无法探测（非 chat 类型/无 key/构造失败），不写列。
//
//	auto 2xx                  → accepted（弱证据 true）
//	auto 4xx 白名单第 1 次      → pending（不写）
//	auto 4xx 白名单 ≥2 次       → unsupported（false）
//	auto 4xx 非白名单 / 5xx    → unknown（不写）
//	required 2xx + tool_call  → executed（强证据 true）
//	required 2xx 无 tool_call → required_ignored（不写）
//	required 4xx             → 降级 auto 对照
func TestToolsSupport(ctx context.Context, channel *appmodel.Channel, modelName, toolChoice string) (appmodel.ToolsProbeResult, error) {
	if channel == nil {
		return appmodel.ToolsProbeResult{}, fmt.Errorf("channel is nil")
	}
	if !outbound.IsChatChannelType(channel.Type) {
		return appmodel.ToolsProbeResult{}, fmt.Errorf("channel type %d is not a chat channel, tools probe skipped", channel.Type)
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return appmodel.ToolsProbeResult{}, fmt.Errorf("model name is empty")
	}
	var usedKey *appmodel.ChannelKey
	for i := range channel.Keys {
		if channel.Keys[i].Enabled && strings.TrimSpace(channel.Keys[i].ChannelKey) != "" {
			usedKey = &channel.Keys[i]
			break
		}
	}
	if usedKey == nil {
		return appmodel.ToolsProbeResult{}, fmt.Errorf("channel %d has no enabled key, tools probe skipped", channel.ID)
	}

	toolsProbeSemaphore <- struct{}{}
	defer func() { <-toolsProbeSemaphore }()

	if toolChoice == "required" {
		return runRequiredToolsProbe(ctx, channel, usedKey, modelName)
	}
	return runAutoToolsProbe(ctx, channel, usedKey, modelName, ""), nil
}

func runRequiredToolsProbe(ctx context.Context, channel *appmodel.Channel, usedKey *appmodel.ChannelKey, modelName string) (appmodel.ToolsProbeResult, error) {
	_, statusCode, body, err := doToolsProbeRequest(ctx, channel, usedKey, modelName, "required")
	if err != nil {
		return appmodel.ToolsProbeResult{State: appmodel.ToolsProbeStateUnknown}, err
	}
	if statusCode >= 200 && statusCode < 300 {
		if responseHasToolCall(body, channel.Type) {
			return appmodel.ToolsProbeResult{State: appmodel.ToolsProbeStateExecuted, Supports: true, Source: "manual"}, nil
		}
		return appmodel.ToolsProbeResult{State: appmodel.ToolsProbeStateRequiredIgnored}, nil
	}
	if statusCode >= 400 && statusCode < 500 {
		return runAutoToolsProbe(ctx, channel, usedKey, modelName, "manual-required-fallback"), nil
	}
	return appmodel.ToolsProbeResult{State: appmodel.ToolsProbeStateUnknown}, nil
}

func runAutoToolsProbe(ctx context.Context, channel *appmodel.Channel, usedKey *appmodel.ChannelKey, modelName, fallbackSource string) appmodel.ToolsProbeResult {
	_, statusCode, body, err := doToolsProbeRequest(ctx, channel, usedKey, modelName, "")
	if err != nil {
		return appmodel.ToolsProbeResult{State: appmodel.ToolsProbeStateUnknown}
	}
	confirmKey := fmt.Sprintf("%d\x00%s", channel.ID, modelName)
	if statusCode >= 200 && statusCode < 300 {
		resetToolsProbeConfirmCount(confirmKey)
		if fallbackSource != "" {
			return appmodel.ToolsProbeResult{State: appmodel.ToolsProbeStateRequiredUnsupported, Supports: true, Source: fallbackSource}
		}
		return appmodel.ToolsProbeResult{State: appmodel.ToolsProbeStateAccepted, Supports: true, Source: "probe"}
	}
	// 只有 4xx 才进白名单累计；5xx/超时是网关故障，不判定。
	if statusCode < 400 || statusCode >= 500 {
		return appmodel.ToolsProbeResult{State: appmodel.ToolsProbeStateUnknown}
	}
	if !matchToolsUnsupportedError(strings.TrimSpace(string(body))) {
		return appmodel.ToolsProbeResult{State: appmodel.ToolsProbeStateUnknown}
	}
	if confirmToolsProbeUnsupported(confirmKey) {
		return appmodel.ToolsProbeResult{State: appmodel.ToolsProbeStateUnsupported, Source: "probe"}
	}
	return appmodel.ToolsProbeResult{State: appmodel.ToolsProbeStatePending}
}

func resetToolsProbeConfirmCount(key string) {
	toolsProbeConfirmCounts.Lock()
	delete(toolsProbeConfirmCounts.counts, key)
	toolsProbeConfirmCounts.Unlock()
}

func confirmToolsProbeUnsupported(key string) bool {
	toolsProbeConfirmCounts.Lock()
	defer toolsProbeConfirmCounts.Unlock()
	toolsProbeConfirmCounts.counts[key]++
	return toolsProbeConfirmCounts.counts[key] >= 2
}

// matchToolsUnsupportedError 判断上游错误文本是否命中 tools 不支持白名单。
func matchToolsUnsupportedError(message string) bool {
	lower := strings.ToLower(message)
	for _, pattern := range toolsProbeUnsupportedPatterns {
		if !strings.Contains(lower, pattern) {
			continue
		}
		// 中文否定语境排除：「不支持 tools 以外的参数」= tools 受支持。
		if strings.Contains(lower, "以外的") {
			return false
		}
		return true
	}
	return false
}

// responseHasToolCall 按协议结构化判断 2xx 响应体是否含工具调用。
func responseHasToolCall(body []byte, channelType outbound.OutboundType) bool {
	if len(body) == 0 {
		return false
	}
	switch channelType {
	case outbound.OutboundTypeAnthropic:
		var resp struct {
			Content []struct {
				Type string `json:"type"`
			} `json:"content"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return false
		}
		for _, block := range resp.Content {
			if block.Type == "tool_use" {
				return true
			}
		}
		return false
	case outbound.OutboundTypeGemini:
		var resp struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						FunctionCall *json.RawMessage `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return false
		}
		for _, cand := range resp.Candidates {
			for _, part := range cand.Content.Parts {
				if part.FunctionCall != nil {
					return true
				}
			}
		}
		return false
	case outbound.OutboundTypeOpenAIResponse:
		var resp struct {
			Output []struct {
				Type string `json:"type"`
			} `json:"output"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return false
		}
		for _, item := range resp.Output {
			if item.Type == "function_call" {
				return true
			}
		}
		return false
	default:
		var resp struct {
			Choices []struct {
				Message *struct {
					ToolCalls []json.RawMessage `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return false
		}
		for _, choice := range resp.Choices {
			if choice.Message != nil && len(choice.Message.ToolCalls) > 0 {
				return true
			}
		}
		return false
	}
}

func doToolsProbeRequest(ctx context.Context, channel *appmodel.Channel, usedKey *appmodel.ChannelKey, modelName, toolChoice string) (*http.Response, int, []byte, error) {
	probeCtx, cancel := context.WithTimeout(ctx, toolsProbeTimeout)
	defer cancel()

	adapter := outbound.Get(channel.Type)
	if adapter == nil {
		return nil, 0, nil, fmt.Errorf("unsupported outbound type: %d", channel.Type)
	}
	internalReq := buildToolsProbeInternalRequest(modelName, toolChoice, channel.Type)
	req, err := adapter.TransformRequest(probeCtx, internalReq, channel.GetNormalizedBaseUrl(), strings.TrimSpace(usedKey.ChannelKey))
	if err != nil {
		return nil, 0, nil, err
	}
	for _, header := range channel.CustomHeader {
		if strings.TrimSpace(header.HeaderKey) != "" {
			req.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}

	httpClient, err := ChannelHttpClient(channel)
	if err != nil {
		return nil, 0, nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, toolsProbeMaxBody))
	return resp, resp.StatusCode, body, nil
}

// buildToolsProbeInternalRequest 构造带最小 function 定义的探测请求。
func buildToolsProbeInternalRequest(modelName, toolChoice string, channelType outbound.OutboundType) *transmodel.InternalLLMRequest {
	stream := false
	ping := "Hi! Please reply with a single short word."
	tool := transmodel.Tool{
		Type: "function",
		Function: transmodel.Function{
			Name:        "get_weather",
			Description: "Get the current weather for a location.",
			Parameters:  []byte(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`),
		},
	}
	tokens := int64(128)

	var toolChoiceRef *transmodel.ToolChoice
	if toolChoice == "required" {
		required := "required"
		toolChoiceRef = &transmodel.ToolChoice{ToolChoice: &required}
	}

	req := &transmodel.InternalLLMRequest{
		Model:      modelName,
		Messages:   []transmodel.Message{{Role: "user", Content: transmodel.MessageContent{Content: &ping}}},
		Stream:     &stream,
		Tools:      []transmodel.Tool{tool},
		ToolChoice: toolChoiceRef,
	}
	switch channelType {
	case outbound.OutboundTypeOpenAIResponse:
		req.RawAPIFormat = transmodel.APIFormatOpenAIResponse
		req.MaxCompletionTokens = &tokens
	case outbound.OutboundTypeAnthropic:
		req.RawAPIFormat = transmodel.APIFormatAnthropicMessage
		req.MaxTokens = &tokens
	case outbound.OutboundTypeGemini:
		req.RawAPIFormat = transmodel.APIFormatGeminiContents
		req.MaxTokens = &tokens
	default:
		req.RawAPIFormat = transmodel.APIFormatOpenAIChatCompletion
		req.MaxTokens = &tokens
	}
	return req
}
