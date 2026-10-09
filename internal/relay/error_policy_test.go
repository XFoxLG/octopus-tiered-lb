package relay

import (
	"errors"
	"net/http"
	"testing"

	dbmodel "github.com/lingyuins/octopus/internal/model"
)

func TestLocationAccessFailureSwitchesChannel(t *testing.T) {
	message := `400: {"error":{"message":"User location is not supported for the API use.","type":"upstream_error","code":400}}`
	def := ClassifyRelayError(http.StatusBadRequest, errors.New(message), false)
	got := applyErrorPolicy(def, &dbmodel.Channel{}, http.StatusBadRequest, message)
	if got.Scope != ScopeNextChannel || !got.IsError {
		t.Fatalf("location rejection must skip the channel, got %+v", got)
	}
	t.Run("committed output", func(t *testing.T) {
		decision := RetryDecision{Scope: ScopeAbortAll, IsError: true, Code: 400}
		if got := applyErrorPolicy(decision, &dbmodel.Channel{}, 400, message); got.Scope != ScopeAbortAll {
			t.Fatalf("must not replay committed output: %+v", got)
		}
	})
	t.Run("explicit policy", func(t *testing.T) {
		if got := applyErrorPolicy(def, &dbmodel.Channel{NonRetryableStatusCodes: "400"}, 400, message); got.Scope != ScopeNone {
			t.Fatalf("explicit no-retry policy must be preserved: %+v", got)
		}
	})
}

// 渠道级可重试码:命中即换渠道,即使默认分类判定为不重试(400)。
func TestApplyErrorPolicyChannelRetryableCodeOverridesDefault(t *testing.T) {
	def := ClassifyRelayError(http.StatusBadRequest, errors.New("400: upstream rejected"), false)
	if def.Scope != ScopeNone {
		t.Fatalf("400 default scope = %s, want none", def.Scope)
	}
	channel := &dbmodel.Channel{RetryableStatusCodes: "400"}
	got := applyErrorPolicy(def, channel, http.StatusBadRequest, "upstream rejected")
	if got.Scope != ScopeNextChannel {
		t.Fatalf("channel retryable code scope = %s, want next_channel", got.Scope)
	}
}

// 渠道级不可重试码优先级最高:即使内置关键词命中,也必须保持不重试。
func TestApplyErrorPolicyNonRetryableBeatsRetryable(t *testing.T) {
	def := ClassifyRelayError(http.StatusBadRequest, errors.New("400"), false)
	channel := &dbmodel.Channel{
		RetryableStatusCodes:    "400",
		NonRetryableStatusCodes: "400",
	}
	got := applyErrorPolicy(def, channel, http.StatusBadRequest, "model not found")
	if got.Scope != ScopeNone {
		t.Fatalf("non-retryable must win, got scope %s", got.Scope)
	}
}

// 渠道级可重试关键词:OR 语义,大小写不敏感子串。
func TestApplyErrorPolicyChannelRetryableKeyword(t *testing.T) {
	def := ClassifyRelayError(http.StatusBadRequest, errors.New("400"), false)
	channel := &dbmodel.Channel{RetryableKeywords: "Upstream Channel Busy"}
	got := applyErrorPolicy(def, channel, http.StatusBadRequest, "error: upstream channel busy, retry later")
	if got.Scope != ScopeNextChannel {
		t.Fatalf("channel keyword scope = %s, want next_channel", got.Scope)
	}
}

// 内置规则:模型别名不存在 → 换渠道(即使上游包成 400)。
func TestApplyErrorPolicyBuiltinChannelKeyword(t *testing.T) {
	def := ClassifyRelayError(http.StatusBadRequest, errors.New("400"), false)
	got := applyErrorPolicy(def, &dbmodel.Channel{}, http.StatusBadRequest,
		`400: {"error":{"message":"model not found","type":"upstream_error"}}`)
	if got.Scope != ScopeNextChannel {
		t.Fatalf("builtin model-not-found scope = %s, want next_channel", got.Scope)
	}
}

// 内置规则:Key 无效 → 换 Key(ScopeSameChannel),而非直接换渠道。
func TestApplyErrorPolicyBuiltinCredentialKeyword(t *testing.T) {
	def := ClassifyRelayError(http.StatusBadRequest, errors.New("400"), false)
	got := applyErrorPolicy(def, &dbmodel.Channel{}, http.StatusBadRequest,
		`400: {"error":{"message":"API key not valid. Please pass a valid API key."}}`)
	if got.Scope != ScopeSameChannel {
		t.Fatalf("builtin invalid-key scope = %s, want same_channel", got.Scope)
	}
}

// 红线:已写出内容(written=true)绝不放宽为可重试。
func TestApplyErrorPolicyNeverReopensCommittedStream(t *testing.T) {
	committed := RetryDecision{Scope: ScopeAbortAll, Reason: "stream already written", Code: 500, IsError: true}
	channel := &dbmodel.Channel{RetryableStatusCodes: "500", RetryableKeywords: "internal server error"}
	got := applyErrorPolicy(committed, channel, 500, "internal server error")
	if got.Scope != ScopeAbortAll {
		t.Fatalf("committed stream must stay aborted, got scope %s", got.Scope)
	}
}

// 请求方错误不被内置规则误伤:「Requests ending with a model turn are not supported」
func TestApplyErrorPolicyKeepsGenuineRequestErrorsNonRetryable(t *testing.T) {
	def := ClassifyRelayError(http.StatusBadRequest, errors.New("400"), false)
	got := applyErrorPolicy(def, &dbmodel.Channel{}, http.StatusBadRequest,
		`400: {"error":{"message":"Requests ending with a model turn are not supported."}}`)
	if got.Scope != ScopeNone {
		t.Fatalf("genuine request error must stay none, got scope %s", got.Scope)
	}
}

// 成功决策不被渠道策略改写。
func TestApplyErrorPolicyLeavesSuccessUntouched(t *testing.T) {
	success := RetryDecision{Scope: ScopeNone, Reason: "success", Code: 200}
	channel := &dbmodel.Channel{RetryableStatusCodes: "200", RetryableKeywords: "ok"}
	got := applyErrorPolicy(success, channel, 200, "ok")
	if got.IsError || got.Scope != ScopeNone {
		t.Fatalf("success must stay success: %+v", got)
	}
}

// 三级分级:确定性溢出语义优先于状态码。
func TestClassifyErrorForClientPrefersDeterministicSemantics(t *testing.T) {
	if got := classifyErrorForClient(500, `context_length_exceeded`); got != ErrorClassRequest {
		t.Fatalf("context overflow class = %s, want request", got)
	}
	if got := classifyErrorForClient(400, `model not found`); got != ErrorClassRequest {
		t.Fatalf("400 class = %s, want request", got)
	}
	if got := classifyErrorForClient(503, `upstream gateway down`); got != ErrorClassUpstream {
		t.Fatalf("503 class = %s, want upstream", got)
	}
}

// 渠道级文案模板:非请求方语义时改写 message,并支持 {upstream} 占位符。
func TestApplyChannelErrorMessageTemplateRewritesUpstreamClass(t *testing.T) {
	message, ok := applyChannelErrorMessageTemplate("上游渠道异常:{upstream}", http.StatusInternalServerError, "boom")
	if !ok {
		t.Fatal("template for upstream class should apply")
	}
	if message != "上游渠道异常:boom" {
		t.Fatalf("template message = %q", message)
	}
}

// 红线:请求方确定性信号不可被模板覆盖,否则下游自动压缩会失效。
func TestApplyChannelErrorMessageTemplateKeepsDeterministicSignals(t *testing.T) {
	for _, text := range []string{"context_length_exceeded", "prompt is too long", "content_filter"} {
		if _, ok := applyChannelErrorMessageTemplate("已改写:{upstream}", http.StatusBadRequest, text); ok {
			t.Fatalf("deterministic signal %q must not be rewritten", text)
		}
	}
	if _, ok := applyChannelErrorMessageTemplate("", http.StatusInternalServerError, "boom"); ok {
		t.Fatal("empty template must not apply")
	}
}

// 渠道级模板:空字符串模板 = 不启用,行为与配置前一致。
func TestChannelErrorMessageTemplateReadsChannelField(t *testing.T) {
	if got := channelErrorMessageTemplate(nil); got != "" {
		t.Fatalf("nil channel template = %q, want empty", got)
	}
	if got := channelErrorMessageTemplate(&dbmodel.Channel{ErrorMessageTemplate: "  x  "}); got != "x" {
		t.Fatalf("channel template = %q, want trimmed x", got)
	}
}
