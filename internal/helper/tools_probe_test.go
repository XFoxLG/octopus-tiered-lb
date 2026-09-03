package helper

import (
	"context"
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestTestToolsSupportRejectsBadParams(t *testing.T) {
	ctx := context.Background()
	valid := &appmodel.Channel{
		Type:     outbound.OutboundTypeOpenAIChat,
		BaseUrls: []appmodel.BaseUrl{{URL: "https://example.com"}},
		Keys:     []appmodel.ChannelKey{{Enabled: true, ChannelKey: "sk-test"}},
	}

	if _, err := TestToolsSupport(ctx, nil, "gpt-4o", ""); err == nil {
		t.Fatal("nil channel: want error, got nil")
	}
	embedding := *valid
	embedding.Type = outbound.OutboundTypeOpenAIEmbedding
	if _, err := TestToolsSupport(ctx, &embedding, "text-embedding-3-small", ""); err == nil {
		t.Fatal("embedding channel: want error, got nil")
	}
	if _, err := TestToolsSupport(ctx, valid, "  ", ""); err == nil {
		t.Fatal("empty model: want error, got nil")
	}
	noKey := *valid
	noKey.Keys = nil
	if _, err := TestToolsSupport(ctx, &noKey, "gpt-4o", ""); err == nil {
		t.Fatal("no key: want error, got nil")
	}
}

func TestMatchToolsUnsupportedError(t *testing.T) {
	cases := []struct {
		message string
		want    bool
	}{
		{"upstream error: 400: tools not supported by this model", true},
		{"400: does not support tool calls", true},
		{"function calling is not supported", true},
		{"模型不支持工具调用", true},
		{"upstream error: 500: internal error", false},
		{"rate limit exceeded", false},
		{"不支持 tools 以外的参数", false}, // 否定语境不命中
		{"", false},
	}
	for _, tc := range cases {
		if got := matchToolsUnsupportedError(tc.message); got != tc.want {
			t.Errorf("matchToolsUnsupportedError(%q) = %v, want %v", tc.message, got, tc.want)
		}
	}
}

func TestResponseHasToolCall(t *testing.T) {
	openAIHit := []byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"1","type":"function"}]}}]}`)
	if !responseHasToolCall(openAIHit, outbound.OutboundTypeOpenAIChat) {
		t.Fatal("openai tool_calls: want true, got false")
	}
	openAIMiss := []byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	if responseHasToolCall(openAIMiss, outbound.OutboundTypeOpenAIChat) {
		t.Fatal("openai no tool_calls: want false, got true")
	}
	anthropicHit := []byte(`{"content":[{"type":"text","text":"hi"},{"type":"tool_use","name":"get_weather"}]}`)
	if !responseHasToolCall(anthropicHit, outbound.OutboundTypeAnthropic) {
		t.Fatal("anthropic tool_use: want true, got false")
	}
	geminiHit := []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather"}}]}}]}`)
	if !responseHasToolCall(geminiHit, outbound.OutboundTypeGemini) {
		t.Fatal("gemini functionCall: want true, got false")
	}
	responsesHit := []byte(`{"output":[{"type":"function_call","name":"get_weather"}]}`)
	if !responseHasToolCall(responsesHit, outbound.OutboundTypeOpenAIResponse) {
		t.Fatal("responses function_call: want true, got false")
	}
	if responseHasToolCall([]byte(`not json`), outbound.OutboundTypeOpenAIChat) {
		t.Fatal("invalid json: want false, got true")
	}
	// 空白变体必须命中（结构化解析相对字符串扫描的优势）。
	spaced := []byte(`{"choices": [{"message": {"tool_calls" : [{"id": "1"}]}}]}`)
	if !responseHasToolCall(spaced, outbound.OutboundTypeOpenAIChat) {
		t.Fatal("openai spaced json: want true, got false")
	}
}

func TestConfirmToolsProbeUnsupportedNeedsTwoHits(t *testing.T) {
	key := "test-confirm\x00model"
	resetToolsProbeConfirmCount(key)
	if confirmToolsProbeUnsupported(key) {
		t.Fatal("first hit: want false, got true")
	}
	if !confirmToolsProbeUnsupported(key) {
		t.Fatal("second hit: want true, got false")
	}
	resetToolsProbeConfirmCount(key)
	if confirmToolsProbeUnsupported(key) {
		t.Fatal("after reset first hit: want false, got true")
	}
	resetToolsProbeConfirmCount(key)
}
