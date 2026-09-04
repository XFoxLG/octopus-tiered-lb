package relay

import (
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestPrepareInternalRequestForOutbound_IsScopedPerChannelAttempt(t *testing.T) {
	first := "first"
	second := "second"
	baseRequest := &transmodel.InternalLLMRequest{
		Model: "gpt-4o-mini",
		Messages: []transmodel.Message{
			{
				Role: "user",
				Content: transmodel.MessageContent{
					MultipleContent: []transmodel.MessageContentPart{
						{Type: "text", Text: &first},
						{Type: "text", Text: &second},
					},
				},
			},
		},
	}

	rewriteChannel := &appmodel.Channel{
		Type: outbound.OutboundTypeOpenAIChat,
		RequestRewrite: &appmodel.RequestRewriteConfig{
			Enabled: true,
			Profile: appmodel.RequestRewriteProfileOpenAIChatCompat,
		},
	}
	plainChannel := &appmodel.Channel{
		Type: outbound.OutboundTypeOpenAIChat,
	}

	rewritten, _, err := prepareInternalRequestForOutbound(rewriteChannel, baseRequest, appmodel.EndpointTypeDeepSeek, nil)
	if err != nil {
		t.Fatalf("prepareInternalRequestForOutbound() rewrite channel error = %v", err)
	}
	plain, _, err := prepareInternalRequestForOutbound(plainChannel, baseRequest, appmodel.EndpointTypeChat, nil)
	if err != nil {
		t.Fatalf("prepareInternalRequestForOutbound() plain channel error = %v", err)
	}

	if rewritten.Messages[0].Content.Content == nil || *rewritten.Messages[0].Content.Content != "first\nsecond" {
		t.Fatalf("rewritten content = %#v, want flattened string", rewritten.Messages[0].Content)
	}
	if plain.Messages[0].Content.Content != nil {
		t.Fatalf("plain channel content = %#v, want original multipart content", plain.Messages[0].Content)
	}
	if len(plain.Messages[0].Content.MultipleContent) != 2 {
		t.Fatalf("plain channel content parts len = %d, want 2", len(plain.Messages[0].Content.MultipleContent))
	}
	if baseRequest.Messages[0].Content.Content != nil {
		t.Fatal("base request was mutated across channel attempts")
	}
	if rewritten.TransformerMetadata[transmodel.TransformerMetadataGroupEndpointType] != appmodel.EndpointTypeDeepSeek {
		t.Fatalf("rewritten transformer metadata = %#v, want deepseek endpoint type", rewritten.TransformerMetadata)
	}
	if plain.TransformerMetadata[transmodel.TransformerMetadataGroupEndpointType] != appmodel.EndpointTypeChat {
		t.Fatalf("plain transformer metadata = %#v, want chat endpoint type", plain.TransformerMetadata)
	}
}

func TestApplyGroupParamOverridePriority(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	channel := &appmodel.Channel{
		Type:          outbound.OutboundTypeOpenAIChat,
		ParamOverride: strPtr(`{"temperature": 0.9, "max_tokens": 100}`),
	}
	group := &appmodel.Group{
		ParamOverride: strPtr(`{"temperature": 0.1, "top_p": 0.5}`),
	}

	// 客户端已设置 temperature：任何覆盖都不生效；max_tokens 取渠道值；top_p 取分组值。
	clientTemp := 0.7
	req := &transmodel.InternalLLMRequest{Temperature: &clientTemp}
	applyParamOverride(channel, group, req)
	if *req.Temperature != 0.7 {
		t.Fatalf("client temperature = %v, want 0.7 (client takes precedence)", *req.Temperature)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 100 {
		t.Fatalf("max_tokens = %#v, want 100 from channel", req.MaxTokens)
	}
	if req.TopP == nil || *req.TopP != 0.5 {
		t.Fatalf("top_p = %#v, want 0.5 from group", req.TopP)
	}

	// 客户端未设置 temperature：渠道覆盖分组。
	req = &transmodel.InternalLLMRequest{}
	applyParamOverride(channel, group, req)
	if req.Temperature == nil || *req.Temperature != 0.9 {
		t.Fatalf("temperature = %#v, want 0.9 from channel (channel beats group)", req.Temperature)
	}

	// 仅分组配置：分组值生效。
	req = &transmodel.InternalLLMRequest{}
	applyParamOverride(&appmodel.Channel{Type: outbound.OutboundTypeOpenAIChat}, group, req)
	if req.TopP == nil || *req.TopP != 0.5 {
		t.Fatalf("top_p = %#v, want 0.5 from group-only", req.TopP)
	}
	if req.MaxTokens != nil {
		t.Fatalf("max_tokens = %#v, want nil (group has no such key)", req.MaxTokens)
	}

	// 非法 JSON：不抛错、不改请求。
	badGroup := &appmodel.Group{ParamOverride: strPtr(`{invalid`)}
	req = &transmodel.InternalLLMRequest{}
	applyParamOverride(&appmodel.Channel{Type: outbound.OutboundTypeOpenAIChat}, badGroup, req)
	if req.TopP != nil || req.MaxTokens != nil {
		t.Fatal("invalid group JSON must leave request untouched")
	}

	// nil group：纯渠道行为不变。
	req = &transmodel.InternalLLMRequest{}
	applyParamOverride(channel, nil, req)
	if req.Temperature == nil || *req.Temperature != 0.9 {
		t.Fatalf("nil group temperature = %#v, want 0.9 from channel", req.Temperature)
	}
}
