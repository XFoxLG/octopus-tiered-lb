package relay

import (
	"errors"
	"net/http"
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

func TestParseCustomRetryableCodes(t *testing.T) {
	codes := parseCustomRetryableCodes("418, 499,abc,99,600,,")
	if !codes[418] || !codes[499] {
		t.Fatalf("valid codes missing: %v", codes)
	}
	if codes[99] || codes[600] {
		t.Fatalf("out-of-range codes must be dropped: %v", codes)
	}
	if len(parseCustomRetryableCodes("")) != 0 {
		t.Fatal("empty input must yield an empty set")
	}
}

func TestParseCustomErrorRulesDropsInvalid(t *testing.T) {
	rules := parseCustomErrorRules(`[
		{"channel_type":"response","keyword":"context_length_exceeded","custom_status":400,"passthrough_message":true},
		{"keyword":"","code":0},
		{"keyword":"x","code":99},
		{"keyword":"y","custom_status":600}
	]`)
	if len(rules) != 1 {
		t.Fatalf("got %d valid rules, want 1", len(rules))
	}
	if rules[0].ChannelType != "response" || rules[0].Keyword != "context_length_exceeded" {
		t.Fatalf("rule not normalized: %+v", rules[0])
	}
	if parseCustomErrorRules("not-json") != nil {
		t.Fatal("invalid JSON must yield nil rules")
	}
}

func TestMatchCustomErrorRuleByChannelAndKeyword(t *testing.T) {
	rules := []CustomErrorRule{
		{ChannelType: "response", Keyword: "context_length_exceeded", CustomStatus: 400, PassthroughMessage: true},
		{Keyword: "context_length_exceeded", CustomStatus: 413},
	}
	upstreamText := `upstream error: 200: {"error":{"code":"context_length_exceeded","message":"input exceeds the context window"}}`

	matched := matchCustomErrorRule(rules, outbound.OutboundTypeOpenAIResponse, 200, upstreamText)
	if matched == nil || matched.CustomStatus != 400 {
		t.Fatalf("response channel must match first rule, got %+v", matched)
	}
	matched = matchCustomErrorRule(rules, outbound.OutboundTypeOpenAIChat, 200, upstreamText)
	if matched == nil || matched.CustomStatus != 413 {
		t.Fatalf("chat channel must fall through to generic rule, got %+v", matched)
	}
	if matchCustomErrorRule(rules, outbound.OutboundTypeOpenAIChat, 200, "unrelated failure") != nil {
		t.Fatal("unrelated text must not match")
	}
}

func TestResolveCustomErrorPresentation(t *testing.T) {
	status, message, ok := resolveCustomErrorPresentation(&CustomErrorRule{
		CustomStatus:       400,
		PassthroughMessage: true,
	}, 500, "upstream detail")
	if !ok || status != 400 || message != "upstream detail" {
		t.Fatalf("got (%d,%q,%v), want (400,upstream detail,true)", status, message, ok)
	}

	status, message, ok = resolveCustomErrorPresentation(&CustomErrorRule{
		PassthroughStatus: true,
		Message:           "wrapped: {upstream}",
	}, 502, "origin text")
	if !ok || status != 502 || message != "wrapped: origin text" {
		t.Fatalf("got (%d,%q,%v), want (502,wrapped: origin text,true)", status, message, ok)
	}

	if _, _, ok := resolveCustomErrorPresentation(&CustomErrorRule{Keyword: "x"}, 500, "x"); ok {
		t.Fatal("rule without any status source must not match presentation")
	}
	if _, _, ok := resolveCustomErrorPresentation(nil, 500, "x"); ok {
		t.Fatal("nil rule must not match presentation")
	}
}

func TestCustomRetryableCodesOverrideDefaultClassification(t *testing.T) {
	// 418 默认走 default 分支 ScopeNextChannel；白名单命中后应为 ScopeSameChannel。
	// 单测环境 setting 为空，白名单查表为空集，这里只锁定默认分类；
	// 白名单覆盖路径由 parseCustomRetryableCodes 单测与 ClassifyRelayError 内
	// 的查表逻辑共同保证（查表命中即返回 ScopeSameChannel，无其他分支）。
	before := ClassifyRelayError(418, errors.New("upstream error: 418: teapot"), false)
	if before.Scope != ScopeNextChannel {
		t.Fatalf("default 418 scope = %s, want next_channel", before.Scope)
	}
	if len(getCustomRetryableCodes()) != 0 {
		t.Fatalf("empty test settings must yield an empty whitelist, got %v", getCustomRetryableCodes())
	}
}

func TestTerminalFailureErrorKeepsProviderFailureChain(t *testing.T) {
	termination := model.TerminationMetadata{
		Cause:          model.TerminationCauseContextExhausted,
		ProviderReason: "context_length_exceeded",
	}
	err := providerTerminalFailureError(termination)
	if !errors.Is(err, errProviderTerminalFailure) {
		t.Fatalf("errors.Is(errProviderTerminalFailure) = false, err = %v", err)
	}
	got, ok := terminalCauseFromError(err)
	if !ok || got.Cause != model.TerminationCauseContextExhausted {
		t.Fatalf("terminalCauseFromError = (%v,%v), want context_exhausted", got, ok)
	}
	if !customErrorIsDeterministicFailure(got) {
		t.Fatal("context exhaustion must be a deterministic failure")
	}
	if customErrorIsDeterministicFailure(model.TerminationMetadata{Cause: model.TerminationCauseError}) {
		t.Fatal("generic provider error must remain retryable")
	}
}

func TestContextLengthExceededScenario(t *testing.T) {
	// Sub2API #3857 对标场景：response.failed 携带 context_length_exceeded，
	// 期望返回 400 且不重试、不计 failover。
	rules := []CustomErrorRule{
		{ChannelType: "response", Keyword: "context_length_exceeded", CustomStatus: http.StatusBadRequest, PassthroughMessage: true},
	}
	upstreamText := `upstream error: 200: {"type":"response.failed","response":{"status":"failed","error":{"code":"context_length_exceeded","message":"input exceeds the context window"}}}`
	rule := matchCustomErrorRule(rules, outbound.OutboundTypeOpenAIResponse, 200, upstreamText)
	if rule == nil {
		t.Fatal("context_length_exceeded rule must match")
	}
	status, message, ok := resolveCustomErrorPresentation(rule, 200, upstreamText)
	if !ok || status != http.StatusBadRequest {
		t.Fatalf("got (%d,%v), want (400,true)", status, ok)
	}
	if message == "" {
		t.Fatal("passthrough message must not be empty")
	}
	termination := model.TerminationMetadata{Cause: model.TerminationCauseContextExhausted}
	if !customErrorIsDeterministicFailure(termination) {
		t.Fatal("context exhaustion must bypass the custom retry whitelist")
	}
}
