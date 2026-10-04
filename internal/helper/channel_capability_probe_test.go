package helper

import (
	"strings"
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// 状态码 → 判定的映射是整套探测的安全边界：Unknown 绝不能写入配置，
// 否则上游一次限流就会把可用协议删掉。
func TestProbeHTTPStatusVerdict(t *testing.T) {
	cases := []struct {
		statusCode int
		want       appmodel.ProbeVerdict
	}{
		{statusCode: 200, want: appmodel.ProbeVerdictPass},
		{statusCode: 201, want: appmodel.ProbeVerdictPass},
		{statusCode: 404, want: appmodel.ProbeVerdictUnsupported},
		{statusCode: 405, want: appmodel.ProbeVerdictUnsupported},
		{statusCode: 401, want: appmodel.ProbeVerdictFail},
		{statusCode: 403, want: appmodel.ProbeVerdictFail},
		{statusCode: 429, want: appmodel.ProbeVerdictUnknown},
		{statusCode: 408, want: appmodel.ProbeVerdictUnknown},
		{statusCode: 500, want: appmodel.ProbeVerdictUnknown},
		{statusCode: 502, want: appmodel.ProbeVerdictUnknown},
		{statusCode: 503, want: appmodel.ProbeVerdictUnknown},
		{statusCode: 504, want: appmodel.ProbeVerdictUnknown},
		{statusCode: 400, want: appmodel.ProbeVerdictFail},
		{statusCode: 422, want: appmodel.ProbeVerdictFail},
	}
	for _, testCase := range cases {
		if got := probeHTTPStatusVerdict(testCase.statusCode); got != testCase.want {
			t.Fatalf("probeHTTPStatusVerdict(%d) = %s, want %s", testCase.statusCode, got, testCase.want)
		}
	}
}

// 限流必须比"不支持"优先判定：一个站点在限流时也可能返回含 "unsupported" 的
// 通用错误页，若被当成 Unsupported 就会把可用协议标成不支持。
func TestClassifyProbeBodyPrefersRateLimitOverUnsupported(t *testing.T) {
	body := `{"error":{"message":"Rate limit exceeded. This model is not supported on your plan."}}`
	got, ok := classifyProbeBody(body)
	if !ok {
		t.Fatal("classifyProbeBody returned ok=false for a rate-limit body")
	}
	if got != appmodel.ProbeVerdictUnknown {
		t.Fatalf("classifyProbeBody = %s, want unknown (limit wins over unsupported)", got)
	}
}

func TestClassifyProbeBodyRecognizesKeywords(t *testing.T) {
	cases := []struct {
		body string
		want appmodel.ProbeVerdict
	}{
		{body: `{"error":"tools are not supported"}`, want: appmodel.ProbeVerdictUnsupported},
		{body: `{"error":"unsupported parameter: response_format"}`, want: appmodel.ProbeVerdictUnsupported},
		{body: `{"error":"不支持该参数"}`, want: appmodel.ProbeVerdictUnsupported},
		{body: `{"error":"invalid api key"}`, want: appmodel.ProbeVerdictFail},
		{body: `{"error":"API key not valid. Please pass a valid API key."}`, want: appmodel.ProbeVerdictFail},
		{body: `{"error":"You have exceeded your requests per minute quota"}`, want: appmodel.ProbeVerdictUnknown},
		{body: `{"error":"您已达到请求数限制"}`, want: appmodel.ProbeVerdictUnknown},
		{body: `{"ok":true}`, want: ""},
		{body: ``, want: ""},
	}
	for _, testCase := range cases {
		got, ok := classifyProbeBody(testCase.body)
		if testCase.want == "" {
			if ok {
				t.Fatalf("classifyProbeBody(%q) = %s, want no keyword verdict", testCase.body, got)
			}
			continue
		}
		if !ok || got != testCase.want {
			t.Fatalf("classifyProbeBody(%q) = (%s, %v), want %s", testCase.body, got, ok, testCase.want)
		}
	}
}

// 探测回显直接来自上游，必须保证不会把密钥落到数据库里。
func TestRedactProbeDetailRemovesSecrets(t *testing.T) {
	secret := "sk-abcdefghijklmnop1234567890"
	body := `{"error":"auth failed for key ` + secret + `","request_id":"req-1"}`
	got := redactProbeDetail(body, secret)
	if strings.Contains(got, secret) {
		t.Fatalf("redactProbeDetail leaked the secret: %s", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("redactProbeDetail did not mark the redaction: %s", got)
	}
	// 没有显式传入密钥时，也要能识别 sk- 形态的 token。
	implicit := redactProbeDetail(`token sk-zzzzzzzzzzzzzzzzzzzz leaked`)
	if strings.Contains(implicit, "sk-zzzzzzzzzzzzzzzzzzzz") {
		t.Fatalf("redactProbeDetail leaked an sk- token: %s", implicit)
	}
}

func TestRedactProbeDetailTruncatesLongBodies(t *testing.T) {
	long := strings.Repeat("x", capabilityProbeMaxDetail*3)
	got := redactProbeDetail(long)
	if len(got) > capabilityProbeMaxDetail+8 {
		t.Fatalf("redactProbeDetail returned %d bytes, want truncation near %d", len(got), capabilityProbeMaxDetail)
	}
}

// HTTP 未通过时，能力判定必须沿用状态码结论，不能靠响应体"猜"。
func TestJudgeCapabilityResponseDefersToHTTPVerdict(t *testing.T) {
	outcome := &probeRequestOutcome{
		Verdict: appmodel.ProbeVerdictUnknown,
		Body:    `{"ok":true,"tool_calls":[{"id":"1"}]}`,
	}
	if got := judgeCapabilityResponse(appmodel.ProbeItemToolCalling, outcome); got != appmodel.ProbeVerdictUnknown {
		t.Fatalf("judgeCapabilityResponse = %s, want the HTTP verdict (unknown)", got)
	}
}

func TestJudgeCapabilityResponseToolCalling(t *testing.T) {
	cases := []struct {
		name string
		body string
		want appmodel.ProbeVerdict
	}{
		{name: "openai tool_calls", body: `{"choices":[{"message":{"tool_calls":[{"id":"1","function":{"name":"verify_tool"}}]}}]}`, want: appmodel.ProbeVerdictPass},
		{name: "anthropic tool_use", body: `{"content":[{"type":"tool_use","name":"verify_tool"}]}`, want: appmodel.ProbeVerdictPass},
		{name: "gemini functionCall", body: `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"verify_tool"}}]}}]}`, want: appmodel.ProbeVerdictPass},
		{name: "empty tool_calls array is not a call", body: `{"choices":[{"message":{"tool_calls":[]}}]}`, want: appmodel.ProbeVerdictFail},
		{name: "refusal", body: `{"choices":[{"message":{"refusal":"I cannot call tools."}}]}`, want: appmodel.ProbeVerdictUnsupported},
		{name: "plain text answer", body: `{"choices":[{"message":{"content":"hello"}}]}`, want: appmodel.ProbeVerdictFail},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			outcome := &probeRequestOutcome{Verdict: appmodel.ProbeVerdictPass, Body: testCase.body}
			if got := judgeCapabilityResponse(appmodel.ProbeItemToolCalling, outcome); got != testCase.want {
				t.Fatalf("judgeCapabilityResponse = %s, want %s (body=%s)", got, testCase.want, testCase.body)
			}
		})
	}
}

func TestJudgeCapabilityResponseStructuredOutput(t *testing.T) {
	pass := &probeRequestOutcome{Verdict: appmodel.ProbeVerdictPass, Body: `{"ok":true}`}
	if got := judgeCapabilityResponse(appmodel.ProbeItemStructuredOutput, pass); got != appmodel.ProbeVerdictPass {
		t.Fatalf("structured output with {ok:true} = %s, want pass", got)
	}
	wrongShape := &probeRequestOutcome{Verdict: appmodel.ProbeVerdictPass, Body: `{"ok":false}`}
	if got := judgeCapabilityResponse(appmodel.ProbeItemStructuredOutput, wrongShape); got != appmodel.ProbeVerdictFail {
		t.Fatalf("structured output with {ok:false} = %s, want fail", got)
	}
	notJSON := &probeRequestOutcome{Verdict: appmodel.ProbeVerdictPass, Body: `Sure! Here is the JSON: ok`}
	if got := judgeCapabilityResponse(appmodel.ProbeItemStructuredOutput, notJSON); got != appmodel.ProbeVerdictFail {
		t.Fatalf("structured output with prose = %s, want fail", got)
	}
}

func TestJudgeCapabilityResponseTextGeneration(t *testing.T) {
	withText := &probeRequestOutcome{Verdict: appmodel.ProbeVerdictPass, Body: `{"choices":[{"message":{"content":"OK"}}]}`}
	if got := judgeCapabilityResponse(appmodel.ProbeItemTextGeneration, withText); got != appmodel.ProbeVerdictPass {
		t.Fatalf("text generation with content = %s, want pass", got)
	}
	empty := &probeRequestOutcome{Verdict: appmodel.ProbeVerdictPass, Body: `{"choices":[]}`}
	if got := judgeCapabilityResponse(appmodel.ProbeItemTextGeneration, empty); got != appmodel.ProbeVerdictFail {
		t.Fatalf("text generation with empty choices = %s, want fail", got)
	}
}

// 联网搜索缺少统一入口，没有检索痕迹时保持 Unknown —— 证据不足不写入。
func TestJudgeCapabilityResponseWebSearchStaysUnknownWithoutEvidence(t *testing.T) {
	prose := &probeRequestOutcome{Verdict: appmodel.ProbeVerdictPass, Body: `{"choices":[{"message":{"content":"Here is a headline"}}]}`}
	if got := judgeCapabilityResponse(appmodel.ProbeItemWebSearch, prose); got != appmodel.ProbeVerdictUnknown {
		t.Fatalf("web search with prose only = %s, want unknown", got)
	}
	withCitations := &probeRequestOutcome{Verdict: appmodel.ProbeVerdictPass, Body: `{"citations":[{"url":"https://example.com"}]}`}
	if got := judgeCapabilityResponse(appmodel.ProbeItemWebSearch, withCitations); got != appmodel.ProbeVerdictPass {
		t.Fatalf("web search with citations = %s, want pass", got)
	}
}

// 原生协议渠道只探自己的原生协议，避免用注定失败的请求浪费额度。
func TestBuildProtocolProbeTargetsPerChannelType(t *testing.T) {
	cases := []struct {
		channelType  outbound.OutboundType
		wantAdapters []outbound.OutboundType
	}{
		{channelType: outbound.OutboundTypeGemini, wantAdapters: []outbound.OutboundType{outbound.OutboundTypeGemini}},
		{channelType: outbound.OutboundTypeAnthropic, wantAdapters: []outbound.OutboundType{outbound.OutboundTypeAnthropic}},
		{channelType: outbound.OutboundTypeVolcengine, wantAdapters: []outbound.OutboundType{outbound.OutboundTypeVolcengine}},
		{channelType: outbound.OutboundTypeCloudflare, wantAdapters: []outbound.OutboundType{outbound.OutboundTypeCloudflare}},
		{
			channelType:  outbound.OutboundTypeOpenAIChat,
			wantAdapters: []outbound.OutboundType{outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse, outbound.OutboundTypeAnthropic},
		},
	}
	for _, testCase := range cases {
		targets := buildProtocolProbeTargets(testCase.channelType, "fixture-model")
		if len(targets) != len(testCase.wantAdapters) {
			t.Fatalf("channelType=%d produced %d targets, want %d",
				testCase.channelType, len(targets), len(testCase.wantAdapters))
		}
		for index, target := range targets {
			if target.AdapterType != testCase.wantAdapters[index] {
				t.Fatalf("channelType=%d target[%d] adapter = %d, want %d",
					testCase.channelType, index, target.AdapterType, testCase.wantAdapters[index])
			}
			if target.Request == nil {
				t.Fatalf("channelType=%d target[%d] request is nil", testCase.channelType, index)
			}
		}
	}
}

// 协议层探测项的请求必须带上正确的入站格式，否则适配器会走错分支。
func TestBuildProtocolProbeTargetsCarryMatchingRawAPIFormat(t *testing.T) {
	targets := buildProtocolProbeTargets(outbound.OutboundTypeOpenAIChat, "fixture-model")
	wantFormats := []transmodel.APIFormat{
		transmodel.APIFormatOpenAIChatCompletion,
		transmodel.APIFormatOpenAIResponse,
		transmodel.APIFormatAnthropicMessage,
	}
	for index, target := range targets {
		if target.Request.RawAPIFormat != wantFormats[index] {
			t.Fatalf("target[%d] RawAPIFormat = %s, want %s", index, target.Request.RawAPIFormat, wantFormats[index])
		}
	}
}

func TestCapabilityProbeSupportedForChannel(t *testing.T) {
	// 嵌入渠道没有对话能力，一律不探。
	if capabilityProbeSupportedForChannel(appmodel.ProbeItemTextGeneration, outbound.OutboundTypeOpenAIEmbedding) {
		t.Fatal("embedding channel should not run chat capability probes")
	}
	// Anthropic 原生渠道没有统一的联网搜索入口。
	if capabilityProbeSupportedForChannel(appmodel.ProbeItemWebSearch, outbound.OutboundTypeAnthropic) {
		t.Fatal("anthropic channel should skip the web search probe")
	}
	// 常规能力在 OpenAI 兼容渠道上应当可探。
	for _, item := range []string{
		appmodel.ProbeItemTextGeneration,
		appmodel.ProbeItemToolCalling,
		appmodel.ProbeItemStructuredOutput,
		appmodel.ProbeItemWebSearch,
	} {
		if !capabilityProbeSupportedForChannel(item, outbound.OutboundTypeOpenAIChat) {
			t.Fatalf("item %s should be probeable on an OpenAI channel", item)
		}
	}
}

// 探测项标识到协议名的映射必须与后端协议词汇一致。
func TestProtocolForProbeItem(t *testing.T) {
	cases := map[string]string{
		appmodel.ProbeItemProtocolChat:      string(appmodel.UpstreamProtocolChatOnly),
		appmodel.ProbeItemProtocolResponses: string(appmodel.UpstreamProtocolResponsesOnly),
		appmodel.ProbeItemProtocolMessages:  string(appmodel.UpstreamProtocolMessagesOnly),
		appmodel.ProbeItemProtocolGemini:    "",
	}
	for item, want := range cases {
		if got := protocolForProbeItem(item); got != want {
			t.Fatalf("protocolForProbeItem(%s) = %q, want %q", item, got, want)
		}
	}
}

// Verdict 的"可写入"判定是只加不减策略的基础。
func TestProbeVerdictIsConclusive(t *testing.T) {
	conclusive := []appmodel.ProbeVerdict{appmodel.ProbeVerdictPass, appmodel.ProbeVerdictUnsupported}
	for _, verdict := range conclusive {
		if !verdict.IsConclusive() {
			t.Fatalf("%s should be conclusive", verdict)
		}
	}
	notConclusive := []appmodel.ProbeVerdict{appmodel.ProbeVerdictFail, appmodel.ProbeVerdictUnknown}
	for _, verdict := range notConclusive {
		if verdict.IsConclusive() {
			t.Fatalf("%s should NOT be conclusive", verdict)
		}
	}
}
