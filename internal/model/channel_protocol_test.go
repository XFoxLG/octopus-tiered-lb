package model

import (
	"reflect"
	"testing"

	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestNormalizeUpstreamProtocolsOrdersAndDeduplicates(t *testing.T) {
	cases := []struct {
		name   string
		input  []string
		want   []string
	}{
		{name: "empty", input: nil, want: nil},
		{name: "blank entries", input: []string{"", "  "}, want: nil},
		{name: "single", input: []string{"chat_only"}, want: []string{"chat_only"}},
		{name: "uppercase and spaces normalized", input: []string{"  Chat_Only  "}, want: []string{"chat_only"}},
		{name: "declaration order preserved", input: []string{"responses", "chat"}, want: []string{"responses", "chat"}},
		{name: "reversed declaration order preserved", input: []string{"chat", "responses"}, want: []string{"chat", "responses"}},
		{name: "duplicates collapse", input: []string{"chat", "CHAT", " chat "}, want: []string{"chat"}},
		{name: "three protocols", input: []string{"messages", "chat", "responses"}, want: []string{"messages", "chat", "responses"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := NormalizeUpstreamProtocols(testCase.input)
			if err != nil {
				t.Fatalf("NormalizeUpstreamProtocols(%v) error = %v", testCase.input, err)
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("NormalizeUpstreamProtocols(%v) = %v, want %v", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestNormalizeUpstreamProtocolsRejectsInvalidValues(t *testing.T) {
	for _, input := range [][]string{
		{"bogus"},
		{"chat", "nonsense"},
		{"truncate"},
	} {
		if _, err := NormalizeUpstreamProtocols(input); err == nil {
			t.Fatalf("NormalizeUpstreamProtocols(%v) error = nil, want validation error", input)
		}
	}
}

func TestNormalizeUpstreamProtocolsRejectsContradictoryCombinations(t *testing.T) {
	cases := [][]string{
		{"passthrough", "chat"},
		{"raw", "responses"},
		{"chat_only", "chat"},
		{"responses_only", "responses"},
		{"messages_only", "messages"},
	}
	for _, input := range cases {
		if _, err := NormalizeUpstreamProtocols(input); err == nil {
			t.Fatalf("NormalizeUpstreamProtocols(%v) error = nil, want mutual-exclusion error", input)
		}
	}
}

func TestResolveTimeoutOverrideFollowsGroupWhenNegative(t *testing.T) {
	cases := []struct {
		name         string
		channelValue int
		groupValue   int
		want         int
	}{
		{name: "zero follows group", channelValue: 0, groupValue: 30, want: 30},
		{name: "zero follows disabled group", channelValue: 0, groupValue: 0, want: 0},
		{name: "negative disables watchdog", channelValue: -1, groupValue: 30, want: 0},
		{name: "negative disables even when group disabled", channelValue: -1, groupValue: 0, want: 0},
		{name: "positive overrides group", channelValue: 15, groupValue: 30, want: 15},
		{name: "positive overrides disabled group", channelValue: 15, groupValue: 0, want: 15},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ResolveTimeoutOverride(testCase.channelValue, testCase.groupValue)
			if got != testCase.want {
				t.Fatalf("ResolveTimeoutOverride(%d, %d) = %d, want %d",
					testCase.channelValue, testCase.groupValue, got, testCase.want)
			}
		})
	}
}

// 渠道三个超时字段全为 -1（迁移默认）时，取值必须与分组完全相同 ——
// 这是「升级当天零行为变化」的核心断言。
func TestChannelTimeoutResolversMatchGroupOnDefaultChannel(t *testing.T) {
	channel := &Channel{}
	if got := channel.EffectiveFirstTokenTimeOut(45); got != 45 {
		t.Fatalf("EffectiveFirstTokenTimeOut on default channel = %d, want 45", got)
	}
	if got := channel.EffectiveAttemptTimeOut(90); got != 90 {
		t.Fatalf("EffectiveAttemptTimeOut on default channel = %d, want 90", got)
	}
	if got := channel.EffectiveStreamIdleTimeout(120); got != 120 {
		t.Fatalf("EffectiveStreamIdleTimeout on default channel = %d, want 120", got)
	}
	// nil 渠道（无渠道上下文的老调用点）同样沿用分组值。
	var nilChannel *Channel
	if got := nilChannel.EffectiveFirstTokenTimeOut(45); got != 45 {
		t.Fatalf("EffectiveFirstTokenTimeOut on nil channel = %d, want 45", got)
	}
}

// 渠道级覆盖真的要生效：显式值必须压过分组值。
func TestChannelTimeoutResolversPreferChannelOverride(t *testing.T) {
	channel := &Channel{FirstTokenTimeOut: 7, AttemptTimeOut: -1, StreamIdleTimeout: 21}
	if got := channel.EffectiveFirstTokenTimeOut(45); got != 7 {
		t.Fatalf("EffectiveFirstTokenTimeOut = %d, want 7", got)
	}
	if got := channel.EffectiveAttemptTimeOut(90); got != 0 {
		t.Fatalf("EffectiveAttemptTimeOut = %d, want 0 (explicitly disabled)", got)
	}
	if got := channel.EffectiveStreamIdleTimeout(120); got != 21 {
		t.Fatalf("EffectiveStreamIdleTimeout = %d, want 21", got)
	}
}

func TestChannelEffectiveReasoningBufferStrategy(t *testing.T) {
	cases := []struct {
		value string
		want  string
	}{
		{value: "", want: ""},
		{value: "buffer", want: "buffer"},
		{value: "Immediate", want: "immediate"},
		{value: "  immediate ", want: "immediate"},
		{value: "bogus", want: ""},
	}
	for _, testCase := range cases {
		channel := &Channel{ReasoningBufferStrategy: testCase.value}
		if got := channel.EffectiveReasoningBufferStrategy(); got != testCase.want {
			t.Fatalf("EffectiveReasoningBufferStrategy(%q) = %q, want %q", testCase.value, got, testCase.want)
		}
	}
}

func TestChannelEffectiveUpstreamProtocols(t *testing.T) {
	if got := (&Channel{}).EffectiveUpstreamProtocols(); got != nil {
		t.Fatalf("EffectiveUpstreamProtocols on empty channel = %v, want nil", got)
	}
	channel := &Channel{UpstreamProtocols: []string{"Responses", "CHAT"}}
	got := channel.EffectiveUpstreamProtocols()
	want := []string{"responses", "chat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EffectiveUpstreamProtocols = %v, want %v", got, want)
	}
	if got := channel.PrimaryUpstreamProtocol(); got != "responses" {
		t.Fatalf("PrimaryUpstreamProtocol = %q, want responses", got)
	}
	// 非法值不得静默生效，应等价于"未声明"（回落分组）。
	invalid := &Channel{UpstreamProtocols: []string{"bogus"}}
	if got := invalid.EffectiveUpstreamProtocols(); got != nil {
		t.Fatalf("EffectiveUpstreamProtocols with invalid value = %v, want nil", got)
	}
}

func TestGetBaseUrlForProtocol(t *testing.T) {
	channel := &Channel{
		BaseUrls: []BaseUrl{
			{URL: "https://ark.example.com", Delay: 50, Protocol: "chat"},
			{URL: "https://generic.example.com", Delay: 10},
			{URL: "https://anthropic.example.com", Delay: 30, Protocol: "messages"},
		},
	}

	if got := channel.GetBaseUrlForProtocol("chat"); got != "https://ark.example.com" {
		t.Fatalf("GetBaseUrlForProtocol(chat) = %q, want the protocol-bound chat URL", got)
	}
	if got := channel.GetBaseUrlForProtocol("messages"); got != "https://anthropic.example.com" {
		t.Fatalf("GetBaseUrlForProtocol(messages) = %q, want the protocol-bound messages URL", got)
	}
	// 未绑定协议 → 回落到通用条目（忽略延迟更低的绑定条目）。
	if got := channel.GetBaseUrlForProtocol("responses"); got != "https://generic.example.com" {
		t.Fatalf("GetBaseUrlForProtocol(responses) = %q, want the generic URL", got)
	}
	// 空协议 → 与旧 GetBaseUrl 完全一致（最低延迟）。
	if got := channel.GetBaseUrlForProtocol(""); got != channel.GetBaseUrl() {
		t.Fatalf("GetBaseUrlForProtocol(\"\") = %q, want to match GetBaseUrl() = %q", got, channel.GetBaseUrl())
	}
}

func TestGetBaseUrlForProtocolPrefersLowestDelayAmongBound(t *testing.T) {
	channel := &Channel{
		BaseUrls: []BaseUrl{
			{URL: "https://slow.example.com", Delay: 80, Protocol: "chat"},
			{URL: "https://fast.example.com", Delay: 5, Protocol: "chat"},
		},
	}
	if got := channel.GetBaseUrlForProtocol("chat"); got != "https://fast.example.com" {
		t.Fatalf("GetBaseUrlForProtocol(chat) = %q, want the lowest-delay bound URL", got)
	}
}

// 单地址（未绑定协议）渠道必须与改动前逐字节一致。
func TestGetBaseUrlForProtocolSingleGenericUrlUnchanged(t *testing.T) {
	channel := &Channel{BaseUrls: []BaseUrl{{URL: "https://api.openai.com", Delay: 0}}}
	for _, protocol := range []string{"", "chat", "responses", "messages"} {
		if got := channel.GetBaseUrlForProtocol(protocol); got != "https://api.openai.com" {
			t.Fatalf("GetBaseUrlForProtocol(%q) = %q, want the single URL", protocol, got)
		}
	}
}

func TestMarshalUpstreamProtocols(t *testing.T) {
	got, err := MarshalUpstreamProtocols([]string{"chat", "responses"})
	if err != nil {
		t.Fatalf("MarshalUpstreamProtocols error = %v", err)
	}
	if got != `["chat","responses"]` {
		t.Fatalf("MarshalUpstreamProtocols = %q", got)
	}
	empty, err := MarshalUpstreamProtocols(nil)
	if err != nil {
		t.Fatalf("MarshalUpstreamProtocols(nil) error = %v", err)
	}
	if empty != "" {
		t.Fatalf("MarshalUpstreamProtocols(nil) = %q, want empty string", empty)
	}
}

// 渠道类型闸门：只有 OpenAI 兼容渠道能承载透传。
func TestSupportsPassthroughFormatGatesNativeChannelTypes(t *testing.T) {
	request := &transmodel.InternalLLMRequest{RawAPIFormat: transmodel.APIFormatOpenAIChatCompletion}
	allowed := []outbound.OutboundType{
		outbound.OutboundTypeOpenAIChat,
		outbound.OutboundTypeOpenAIResponse,
		outbound.OutboundTypeMimo,
	}
	for _, channelType := range allowed {
		if !outbound.SupportsPassthroughFormat(channelType, request) {
			t.Fatalf("SupportsPassthroughFormat(%d) = false, want true", channelType)
		}
	}
	denied := []outbound.OutboundType{
		outbound.OutboundTypeAnthropic,
		outbound.OutboundTypeGemini,
		outbound.OutboundTypeVolcengine,
		outbound.OutboundTypeCloudflare,
		outbound.OutboundTypeOpenAIEmbedding,
		outbound.OutboundTypeCodex,
	}
	for _, channelType := range denied {
		if outbound.SupportsPassthroughFormat(channelType, request) {
			t.Fatalf("SupportsPassthroughFormat(%d) = true, want false", channelType)
		}
	}
	if outbound.SupportsPassthroughFormat(outbound.OutboundTypeOpenAIChat, nil) {
		t.Fatal("SupportsPassthroughFormat with nil request = true, want false")
	}
}
