package relay

import (
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestOutboundAttemptTypesChatOnChatChannelAutoPrefersChat(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "", "")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesChatOnResponseChannelAutoPrefersChat(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIResponse, req, "", "")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesResponsesOnChatChannelAutoPrefersChat(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIResponse}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "", "")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesResponsesOnResponseChannelAutoPrefersChat(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIResponse}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIResponse, req, "", "")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesEmbeddingNoFallback(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIEmbedding}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "", "")
	if len(got) != 1 || got[0] != outbound.OutboundTypeOpenAIChat {
		t.Fatalf("attempt types = %#v, want single channel type", got)
	}
}

func TestOutboundAttemptTypesNilRequest(t *testing.T) {
	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, nil, "", "")
	if len(got) != 1 || got[0] != outbound.OutboundTypeOpenAIChat {
		t.Fatalf("attempt types = %#v, want single channel type", got)
	}
}

func TestOutboundAttemptTypesChatFormatPrefersChatFirst(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "chat", "")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesResponsesFormatPrefersResponseFirst(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "responses", "")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIResponse, outbound.OutboundTypeOpenAIChat}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesChatOnlyDisablesFallback(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "chat_only", "")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIChat}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesResponsesOnlyDisablesFallback(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "responses_only", "")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIResponse}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesMessagesFormatPrefersAnthropicFirst(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "messages", "")
	want := []outbound.OutboundType{outbound.OutboundTypeAnthropic, outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesMessagesOnlyDisablesFallback(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "messages_only", "")
	want := []outbound.OutboundType{outbound.OutboundTypeAnthropic}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesPassthroughDisablesFallback(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "passthrough", "")
	want := []outbound.OutboundType{outbound.OutboundTypePassthrough}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesAnthropicMessagesOnlyUsesAnthropic(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatAnthropicMessage}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "messages_only", "")
	want := []outbound.OutboundType{outbound.OutboundTypeAnthropic}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesAnthropicMessagesPrefersAnthropicFirst(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatAnthropicMessage}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "messages", "")
	want := []outbound.OutboundType{outbound.OutboundTypeAnthropic, outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesAnthropicPassthroughDisablesFallback(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatAnthropicMessage}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "passthrough", "")
	want := []outbound.OutboundType{outbound.OutboundTypePassthrough}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesRawPassthroughUsesRawAdapter(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "raw", "")
	want := []outbound.OutboundType{outbound.OutboundTypeRaw}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesAnthropicRawPassthroughUsesRawAdapter(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatAnthropicMessage}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "raw", "")
	want := []outbound.OutboundType{outbound.OutboundTypeRaw}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesAnthropicAutoStillPrefersChatFallbackChain(t *testing.T) {
	// Anthropic inbound + auto format on an OpenAI-compatible channel should still
	// enter the LLM adapter-selection path instead of hard-coding channel type.
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatAnthropicMessage}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "", "")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

// 分组级 passthrough/raw 会应用到分组内所有渠道。原生协议渠道（Gemini /
// Anthropic / 火山 / Cloudflare / Embedding）无法被透传适配器承载，必须回落到
// 自己的原生适配器，否则请求体会以 OpenAI 形状发到原生端点，必然失败。
func TestOutboundAttemptTypesPassthroughFallsBackOnNativeChannelTypes(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	for _, channelType := range []outbound.OutboundType{
		outbound.OutboundTypeAnthropic,
		outbound.OutboundTypeGemini,
		outbound.OutboundTypeVolcengine,
		outbound.OutboundTypeCloudflare,
		outbound.OutboundTypeOpenAIEmbedding,
		outbound.OutboundTypeCodex,
	} {
		got := outboundAttemptTypes(channelType, req, "passthrough", "")
		want := []outbound.OutboundType{channelType}
		if len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("channelType=%d passthrough attempt types = %#v, want %#v", channelType, got, want)
		}
	}
}

func TestOutboundAttemptTypesRawFallsBackOnNativeChannelTypes(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	for _, channelType := range []outbound.OutboundType{
		outbound.OutboundTypeAnthropic,
		outbound.OutboundTypeGemini,
		outbound.OutboundTypeVolcengine,
		outbound.OutboundTypeCloudflare,
		outbound.OutboundTypeOpenAIEmbedding,
		outbound.OutboundTypeCodex,
	} {
		got := outboundAttemptTypes(channelType, req, "raw", "")
		want := []outbound.OutboundType{channelType}
		if len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("channelType=%d raw attempt types = %#v, want %#v", channelType, got, want)
		}
	}
}

// 每种入站格式 × 每种可透传渠道类型都必须仍然走透传，保证这次闸门没有
// 把原本正常的 OpenAI 兼容渠道一起收紧掉。
func TestOutboundAttemptTypesPassthroughStillAppliesToOpenAICompatibleChannels(t *testing.T) {
	for _, rawFormat := range []model.APIFormat{
		model.APIFormatOpenAIChatCompletion,
		model.APIFormatOpenAIResponse,
		model.APIFormatAnthropicMessage,
	} {
		for _, channelType := range []outbound.OutboundType{
			outbound.OutboundTypeOpenAIChat,
			outbound.OutboundTypeOpenAIResponse,
			outbound.OutboundTypeMimo,
		} {
			req := &model.InternalLLMRequest{RawAPIFormat: rawFormat}

			got := outboundAttemptTypes(channelType, req, "passthrough", "")
			if len(got) != 1 || got[0] != outbound.OutboundTypePassthrough {
				t.Fatalf("format=%s channelType=%d passthrough = %#v, want [passthrough]", rawFormat, channelType, got)
			}

			gotRaw := outboundAttemptTypes(channelType, req, "raw", "")
			if len(gotRaw) != 1 || gotRaw[0] != outbound.OutboundTypeRaw {
				t.Fatalf("format=%s channelType=%d raw = %#v, want [raw]", rawFormat, channelType, gotRaw)
			}
		}
	}
}

// 非 LLM 入站格式（如 embedding）不能被透传接管，即使渠道类型是 OpenAI Chat。
func TestOutboundAttemptTypesPassthroughSkipsNonLLMFormats(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIEmbedding}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "passthrough", "")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIChat}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("passthrough on embedding format = %#v, want %#v", got, want)
	}
}

// nil 请求不得 panic，且必须回落到渠道自身适配器。
func TestOutboundAttemptTypesPassthroughHandlesNilRequest(t *testing.T) {
	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, nil, "passthrough", "")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIChat}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("passthrough with nil request = %#v, want %#v", got, want)
	}
}

// Unknown format values fall back to the default auto behavior so a stale or
// mistyped setting never disables routing entirely.
func TestOutboundAttemptTypesUnknownFormatFallsBackToAuto(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "bogus", "")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIChat, outbound.OutboundTypeOpenAIResponse}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesChannelOverrideBeatsGroupFormat(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "responses", "chat_only")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIChat}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesChannelOverrideResponsesOnlyBeatsGroupChatOnly(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "chat_only", "responses_only")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIResponse}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesChannelOverrideInvalidFallsBackToGroup(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "responses", "bogus")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIResponse, outbound.OutboundTypeOpenAIChat}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestOutboundAttemptTypesChannelOverrideEmptyFallsBackToGroup(t *testing.T) {
	req := &model.InternalLLMRequest{RawAPIFormat: model.APIFormatOpenAIChatCompletion}

	got := outboundAttemptTypes(outbound.OutboundTypeOpenAIChat, req, "responses", "  ")
	want := []outbound.OutboundType{outbound.OutboundTypeOpenAIResponse, outbound.OutboundTypeOpenAIChat}

	if len(got) != len(want) {
		t.Fatalf("attempt types len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt types = %#v, want %#v", got, want)
		}
	}
}

func TestShouldTryAdapterFallbackSkipsSameChannelFailures(t *testing.T) {
	result := attemptResult{
		Success:  false,
		Written:  false,
		Decision: RetryDecision{Scope: ScopeSameChannel, Reason: "unauthorized", Code: 401, IsError: true},
	}

	if shouldTryAdapterFallback(result, 0, 2) {
		t.Fatal("expected key-scoped failure to skip adapter fallback")
	}
}

func TestShouldTryAdapterFallbackAllowsNextChannelFailures(t *testing.T) {
	result := attemptResult{
		Success:  false,
		Written:  false,
		Decision: RetryDecision{Scope: ScopeNextChannel, Reason: "gateway error", Code: 503, IsError: true},
	}

	if !shouldTryAdapterFallback(result, 0, 2) {
		t.Fatal("expected route-scoped failure to allow adapter fallback")
	}
}

func TestShouldTryAdapterFallbackSkipsClientErrorScopeNone(t *testing.T) {
	result := attemptResult{
		Success:  false,
		Written:  false,
		Decision: RetryDecision{Scope: ScopeNone, Reason: "bad request, client error", Code: 400, IsError: true},
	}

	if shouldTryAdapterFallback(result, 0, 2) {
		t.Fatal("expected client error ScopeNone to skip adapter fallback so upstream body can be returned immediately")
	}
}
