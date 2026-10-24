package relay

import (
	"testing"

	appmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
)

// REQ-02 attempt 隔离：prepareInternalRequestForOutbound 是每次 attempt 的
// 出站入口。多次 attempt 复用同一个只读请求源时，第一次注入（param_override、
// 分组思考档位）不得污染请求源，否则第二次 attempt 会把污染值当成
// 「客户端已设置」而拒绝自己的覆盖。
func TestPrepareAttemptIsolationParamOverrideAcrossChannels(t *testing.T) {
	base := &transmodel.InternalLLMRequest{
		Model:    "creative-fixture",
		Messages: []transmodel.Message{{Role: "user"}},
	}
	channelA := &appmodel.Channel{
		Type:          outbound.OutboundTypeOpenAIChat,
		ParamOverride: strPtrRelay(`{"max_tokens":100}`),
	}
	channelB := &appmodel.Channel{
		Type:          outbound.OutboundTypeOpenAIChat,
		ParamOverride: strPtrRelay(`{"max_tokens":200}`),
	}

	first, _, err := prepareInternalRequestForOutbound(channelA, base, "chat", nil)
	if err != nil {
		t.Fatalf("channel A prepare error: %v", err)
	}
	if first.MaxTokens == nil || *first.MaxTokens != 100 {
		t.Fatalf("channel A MaxTokens = %v, want 100", first.MaxTokens)
	}
	if base.MaxTokens != nil {
		t.Fatalf("request source was mutated by channel A: MaxTokens = %v", *base.MaxTokens)
	}

	second, _, err := prepareInternalRequestForOutbound(channelB, base, "chat", nil)
	if err != nil {
		t.Fatalf("channel B prepare error: %v", err)
	}
	if second.MaxTokens == nil || *second.MaxTokens != 200 {
		t.Fatalf("channel B MaxTokens = %v, want 200; first attempt leaked into the shared request", second.MaxTokens)
	}
}

func TestPrepareAttemptIsolationReasoningPolicyAcrossAttempts(t *testing.T) {
	base := &transmodel.InternalLLMRequest{
		Model:    "creative-fixture",
		Messages: []transmodel.Message{{Role: "user"}},
	}
	channel := &appmodel.Channel{Type: outbound.OutboundTypeOpenAIChat}
	highGroup := &appmodel.Group{DefaultReasoningEffort: "high"}
	lowGroup := &appmodel.Group{DefaultReasoningEffort: "low"}

	first, _, err := prepareInternalRequestForOutbound(channel, base, "chat", highGroup)
	if err != nil {
		t.Fatalf("high group prepare error: %v", err)
	}
	if first.ReasoningEffort != "high" {
		t.Fatalf("high group ReasoningEffort = %q, want high", first.ReasoningEffort)
	}
	if base.ReasoningEffort != "" {
		t.Fatalf("request source was mutated by first attempt: ReasoningEffort = %q", base.ReasoningEffort)
	}

	second, _, err := prepareInternalRequestForOutbound(channel, base, "chat", lowGroup)
	if err != nil {
		t.Fatalf("low group prepare error: %v", err)
	}
	if second.ReasoningEffort != "low" {
		t.Fatalf("low group ReasoningEffort = %q, want low; first attempt leaked into the shared request", second.ReasoningEffort)
	}
}

func strPtrRelay(v string) *string {
	return &v
}
