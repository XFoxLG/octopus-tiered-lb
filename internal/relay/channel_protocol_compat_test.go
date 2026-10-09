package relay

import (
	"reflect"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// 渠道未声明协议（升级后的默认状态）时，解析结果必须与改动前逐字节一致。
// 这组用例把旧行为固化成期望值：任何"下沉"改动都不该改变未配置渠道的路由。
func TestOutboundAttemptTypesForChannelWithNoDeclarationMatchesLegacyResolution(t *testing.T) {
	rawFormats := []model.APIFormat{
		model.APIFormatOpenAIChatCompletion,
		model.APIFormatOpenAIResponse,
		model.APIFormatAnthropicMessage,
	}
	channelTypes := []outbound.OutboundType{
		outbound.OutboundTypeOpenAIChat,
		outbound.OutboundTypeOpenAIResponse,
		outbound.OutboundTypeMimo,
		outbound.OutboundTypeAnthropic,
		outbound.OutboundTypeGemini,
		outbound.OutboundTypeVolcengine,
		outbound.OutboundTypeCloudflare,
	}
	groupFormats := []string{"", "chat", "responses", "messages", "chat_only", "responses_only", "messages_only", "passthrough", "raw", "bogus"}
	channelOverrides := []string{"", "chat_only", "responses_only", "bogus"}

	for _, rawFormat := range rawFormats {
		for _, channelType := range channelTypes {
			for _, groupFormat := range groupFormats {
				for _, override := range channelOverrides {
					request := &model.InternalLLMRequest{RawAPIFormat: rawFormat}

					legacy := outboundAttemptTypes(channelType, request, groupFormat, override)
					declared := outboundAttemptTypesForChannel(channelType, request, groupFormat, override, nil)

					if !reflect.DeepEqual(legacy, declared) {
						t.Fatalf(
							"channel fields empty changed routing: rawFormat=%s channelType=%d groupFormat=%q override=%q legacy=%v declared=%v",
							rawFormat, channelType, groupFormat, override, legacy, declared,
						)
					}
				}
			}
		}
	}
}

// 声明为空切片（前端"清空勾选"的序列化结果）也必须等价于未声明。
func TestOutboundAttemptTypesForChannelWithEmptyDeclarationMatchesLegacy(t *testing.T) {
	request := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}
	legacy := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, request, "responses", "chat_only")
	declared := outboundAttemptTypesForChannel(outbound.OutboundTypeOpenAIChat, request, "responses", "chat_only", []string{})
	if !reflect.DeepEqual(legacy, declared) {
		t.Fatalf("empty declaration changed routing: legacy=%v declared=%v", legacy, declared)
	}
}

// 单协议声明退化成对应的严格模式：只试那一个 adapter，不补回退项。
func TestOutboundAttemptTypesForChannelSingleStrictProtocolHasNoFallback(t *testing.T) {
	request := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}
	cases := []struct {
		protocols []string
		want      []outbound.OutboundType
	}{
		{protocols: []string{"chat_only"}, want: []outbound.OutboundType{outbound.OutboundTypeOpenAIChat}},
		{protocols: []string{"responses_only"}, want: []outbound.OutboundType{outbound.OutboundTypeOpenAIResponse}},
		{protocols: []string{"messages_only"}, want: []outbound.OutboundType{outbound.OutboundTypeAnthropic}},
	}
	for _, testCase := range cases {
		got := outboundAttemptTypesForChannel(outbound.OutboundTypeOpenAIChat, request, "", "", testCase.protocols)
		if !reflect.DeepEqual(got, testCase.want) {
			t.Fatalf("protocols=%v got=%v want=%v", testCase.protocols, got, testCase.want)
		}
	}
}

// 多协议声明按用户勾选顺序展开，并去重。
func TestOutboundAttemptTypesForChannelMultiProtocolFollowsDeclarationOrder(t *testing.T) {
	request := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}
	cases := []struct {
		name      string
		protocols []string
		want      []outbound.OutboundType
	}{
		{
			name:      "chat then responses",
			protocols: []string{"chat", "responses"},
			want:      []outbound.OutboundType{outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse},
		},
		{
			name:      "responses then chat",
			protocols: []string{"responses", "chat"},
			want:      []outbound.OutboundType{outbound.OutboundTypeOpenAIResponse, outbound.OutboundTypeOpenAIChat},
		},
		{
			name:      "two strict protocols",
			protocols: []string{"responses_only", "chat_only"},
			want:      []outbound.OutboundType{outbound.OutboundTypeOpenAIResponse, outbound.OutboundTypeOpenAIChat},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := outboundAttemptTypesForChannel(outbound.OutboundTypeOpenAIChat, request, "", "", testCase.protocols)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("protocols=%v got=%v want=%v", testCase.protocols, got, testCase.want)
			}
		})
	}
}

// 渠道声明的 passthrough 对原生协议渠道必须被闸门拦下，回落自己的适配器。
func TestOutboundAttemptTypesForChannelPassthroughDeclarationRespectsGates(t *testing.T) {
	request := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	// OpenAI 兼容渠道：透传生效。
	got := outboundAttemptTypesForChannel(outbound.OutboundTypeOpenAIChat, request, "", "", []string{"passthrough"})
	if !reflect.DeepEqual(got, []outbound.OutboundType{outbound.OutboundTypePassthrough}) {
		t.Fatalf("OpenAI channel with passthrough declaration = %v, want [passthrough]", got)
	}

	// 原生渠道：透传被拦下，回落自身适配器。
	for _, channelType := range []outbound.OutboundType{
		outbound.OutboundTypeGemini,
		outbound.OutboundTypeAnthropic,
		outbound.OutboundTypeVolcengine,
		outbound.OutboundTypeCloudflare,
	} {
		got := outboundAttemptTypesForChannel(channelType, request, "", "", []string{"raw"})
		if !reflect.DeepEqual(got, []outbound.OutboundType{channelType}) {
			t.Fatalf("channelType=%d with raw declaration = %v, want [%d]", channelType, got, channelType)
		}
	}
}

// 渠道协议声明必须压过分组的出站格式（这就是"下沉"的目的）。
func TestOutboundAttemptTypesForChannelDeclarationBeatsGroupFormat(t *testing.T) {
	request := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}
	got := outboundAttemptTypesForChannel(
		outbound.OutboundTypeOpenAIChat,
		request,
		"passthrough", // 分组说透传
		"",
		[]string{"chat_only"}, // 渠道说自己只支持 Chat
	)
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIChat}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("channel declaration did not win over group format: got=%v want=%v", got, want)
	}
}
