package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	dbmodel "github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/setting"
	"github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
)

// CustomErrorRule 是一条自定义错误透传规则（对标 Sub2API 的“错误透传规则”）。
// 全局一张表，规则内按渠道类型区分：同一关键词在不同渠道类型下可以有不同的
// 自定义状态码与透传行为。
//
// 示例：
//   {"channel_type":"response","keyword":"context_length_exceeded","custom_status":400,"passthrough_message":true}
type CustomErrorRule struct {
	// ChannelType 是渠道类型字符串（见 outbound.OutboundType.String，如
	// "chat"/"response"/"anthropic"/"gemini"）。空串表示匹配所有渠道类型。
	ChannelType string `json:"channel_type,omitempty"`
	// Code 是上游错误码（HTTP 状态码）。0 表示不按错误码匹配。
	Code int `json:"code,omitempty"`
	// Keyword 是上游错误文本中的关键词（大小写不敏感的子串匹配）。空串表示
	// 不按关键词匹配。Code 与 Keyword 至少要填一个，否则规则无意义。
	Keyword string `json:"keyword,omitempty"`
	// PassthroughStatus 为 true 时，最终返回上游原始状态码；为 false 时使用
	// CustomStatus（0 则回退到默认的 502）。
	PassthroughStatus bool `json:"passthrough_status,omitempty"`
	// CustomStatus 是最终返回给客户端的 HTTP 状态码（100-599）。
	CustomStatus int `json:"custom_status,omitempty"`
	// PassthroughMessage 为 true 时，最终 message 透传上游原文；为 false 时
	// 使用 Message 模板（空则回退默认文案）。
	PassthroughMessage bool `json:"passthrough_message,omitempty"`
	// Message 是自定义错误文案模板，支持 {upstream} 占位符嵌入上游原文片段。
	Message string `json:"message,omitempty"`
}

// validCustomErrorStatus 报告状态码是否在合法 HTTP 范围内。
func validCustomErrorStatus(status int) bool {
	return status >= 100 && status <= 599
}

// builtinRetryableErrorKeywords 是内置智能默认规则。公益站与私有网关常把
// 「模型别名不存在 / 该 Key 无此模型权限 / Key 无效 / 端点不支持」这类本可换
// 渠道解决的失败包成 HTTP 400,导致默认分类直接终态透传给客户端造成中断。
// 命中这些语义时强制进入换 Key/换渠道重试,让网关自愈。
//
// 只在「换渠道可能改变结果」的语义上放宽;context_length_exceeded、内容过滤、
// 拒答等确定性失败在 attempt() 中已优先豁免,永远不会走到这里。
// 刻意不含泛化的 "not supported" —— 那会误伤「Requests ending with a model
// turn are not supported」这类真正的请求方错误。
// builtinCredentialRetryKeywords 指向「换一把 Key 就可能成功」的语义。
// 命中后按 ScopeSameChannel 处理:先在同一渠道内轮换 Key,用完再自然换渠道。
var builtinCredentialRetryKeywords = []string{
	"api key not valid",
	"invalid api key",
	"incorrect api key",
	"invalid_api_key",
	"api key is invalid",
	"invalid token",
	"token is invalid",
	"authentication failed",
	"密钥无效",
	"密钥错误",
	"令牌无效",
	"认证失败",
}

// builtinChannelRetryKeywords 指向「换一个渠道才可能成功」的语义:模型别名、
// 模型权限、端点支持、渠道可用性。命中后按 ScopeNextChannel 处理。
// 刻意不含泛化的 "not supported" —— 那会误伤「Requests ending with a model
// turn are not supported」这类真正的请求方错误。
var builtinChannelRetryKeywords = []string{
	"model not found",
	"model_not_found",
	"no such model",
	"unknown model",
	"unsupported model",
	"model is not supported",
	"model not supported",
	"not have access to model",
	"model does not exist",
	"the model does not exist",
	"endpoint not supported",
	"unsupported endpoint",
	"channel not available",
	"no available channel",
	"upstream unavailable",
	"all available accounts exhausted",
	"no capacity available",
	"模型不存在",
	"该模型不存在",
	"模型未开通",
	"不支持的模型",
	"无该模型权限",
	"模型无权限",
	"没有权限使用",
	"渠道不可用",
	"渠道不存在",
	"服务暂不可用",
}

// matchBuiltinRetryScope 报告上游错误文本命中的内置可重试语义及其作用域。
// 凭据类优先(keyScope 更具体);命中返回 (scope, keyword, true)。
func matchBuiltinRetryScope(upstreamText string) (RetryScope, string, bool) {
	lowered := strings.ToLower(strings.TrimSpace(upstreamText))
	if lowered == "" {
		return ScopeNone, "", false
	}
	for _, keyword := range builtinCredentialRetryKeywords {
		if strings.Contains(lowered, strings.ToLower(keyword)) {
			return ScopeSameChannel, keyword, true
		}
	}
	for _, keyword := range builtinChannelRetryKeywords {
		if strings.Contains(lowered, strings.ToLower(keyword)) {
			return ScopeNextChannel, keyword, true
		}
	}
	return ScopeNone, "", false
}

// applyErrorPolicy 在默认分类之上叠加渠道级错误策略与内置智能默认规则。
//
// 设计约束:
//   - written(已向下游写出内容)绝不放宽为可重试,这是不可破的红线(防两段回答拼接)。
//   - 渠道显式配置的不可重试码优先级最高。
//   - 放宽方向永远是「不再重试 → 可重试」;默认已可重试的决策保持不动。
//   - 确定性失败(如 context_length_exceeded)在调用方已提前终态,不会到这里。
func applyErrorPolicy(decision RetryDecision, ch *dbmodel.Channel, statusCode int, upstreamText string) RetryDecision {
	if decision.Scope == ScopeAbortAll || !decision.IsError {
		return decision
	}

	policy := parseChannelErrorPolicy(ch)
	if policy.matchesNonRetryable(statusCode) {
		return RetryDecision{
			Scope:   ScopeNone,
			Reason:  fmt.Sprintf("channel non-retryable status code %d", statusCode),
			Code:    statusCode,
			IsError: true,
		}
	}

	// 渠道级配置:用户说得很明确,命中即换渠道。
	if policy.matchesRetryable(statusCode, upstreamText) {
		return RetryDecision{
			Scope:   ScopeNextChannel,
			Reason:  fmt.Sprintf("channel retryable rule matched (status %d)", statusCode),
			Code:    statusCode,
			IsError: true,
		}
	}

	// 内置规则只在默认分类已判定「不再重试」时介入,避免覆盖更精细的既有决策。
	if decision.Scope != ScopeNone {
		return decision
	}
	if scope, keyword, ok := matchBuiltinRetryScope(upstreamText); ok {
		return RetryDecision{
			Scope:   scope,
			Reason:  fmt.Sprintf("builtin retryable keyword matched: %s", keyword),
			Code:    statusCode,
			IsError: true,
		}
	}
	return decision
}

// channelErrorPolicy 是渠道级错误策略的解析结果。所有字段为空时表示该渠道
// 未配置任何覆盖,行为与配置前完全一致。
type channelErrorPolicy struct {
	retryableCodes    map[int]bool
	retryableKeywords []string
	nonRetryableCodes map[int]bool
	messageTemplate   string
}

// parseChannelErrorPolicy 解析渠道级错误策略字段。任一解析失败都保守降级为
// 「该项未配置」,绝不让脏数据放大成「意外重试」或「意外不重试」。
func parseChannelErrorPolicy(ch *dbmodel.Channel) channelErrorPolicy {
	policy := channelErrorPolicy{}
	if ch == nil {
		return policy
	}
	if codes := parseCustomRetryableCodes(ch.RetryableStatusCodes); len(codes) > 0 {
		policy.retryableCodes = codes
	}
	if codes := parseCustomRetryableCodes(ch.NonRetryableStatusCodes); len(codes) > 0 {
		policy.nonRetryableCodes = codes
	}
	for _, part := range strings.Split(ch.RetryableKeywords, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			policy.retryableKeywords = append(policy.retryableKeywords, part)
		}
	}
	policy.messageTemplate = strings.TrimSpace(ch.ErrorMessageTemplate)
	return policy
}

// matchesRetryable 报告该策略是否要求强制重试。
func (p channelErrorPolicy) matchesRetryable(statusCode int, upstreamText string) bool {
	if p.retryableCodes != nil && p.retryableCodes[statusCode] {
		return true
	}
	if len(p.retryableKeywords) == 0 {
		return false
	}
	lowered := strings.ToLower(upstreamText)
	for _, keyword := range p.retryableKeywords {
		if strings.Contains(lowered, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

// matchesNonRetryable 报告该策略是否要求强制不重试。优先级高于可重试规则。
func (p channelErrorPolicy) matchesNonRetryable(statusCode int) bool {
	return p.nonRetryableCodes != nil && p.nonRetryableCodes[statusCode]
}

// parseCustomRetryableCodes 解析逗号分隔的自定义可重试状态码。空串返回空集；
// 非法片段直接丢弃（Validate 已在写入时拦截，运行时读到脏数据也不炸）。
// ErrorClass 是面向下游的粗粒度错误分级。它只影响最终呈现文案,不改重试决策、
// 不伪造成功、不替换上游可识别信号(context_length_exceeded 等关键字段必须原样保留)。
type ErrorClass string

const (
	// ErrorClassRequest 表示请求方问题:重试无用,需要用户改请求或找管理员。
	ErrorClassRequest ErrorClass = "request"
	// ErrorClassUpstream 表示上游渠道问题:网关已尽力切换,用户可选重试。
	ErrorClassUpstream ErrorClass = "upstream"
	// ErrorClassUnavailable 表示全部渠道都不可用(自愈失败)。
	ErrorClassUnavailable ErrorClass = "unavailable"
)

// classifyErrorForClient 按状态码与错误文本给出面向下游的分级。
// 语义确定性优先于状态码:即使上游把溢出错误包成 500,也判为请求方问题。
func classifyErrorForClient(statusCode int, upstreamText string) ErrorClass {
	lowered := strings.ToLower(upstreamText)
	// 可识别的请求方语义:重试必然同样失败,必须让客户端看到关键信号。
	for _, marker := range []string{
		"context_length_exceeded",
		"context length exceeded",
		"maximum context length",
		"prompt is too long",
		"too many tokens",
		"context window",
		"content_filter",
		"content filter",
		"content_policy",
		"input exceeds",
		"invalid_request_error",
		"requests ending with a model turn",
	} {
		if strings.Contains(lowered, marker) {
			return ErrorClassRequest
		}
	}
	switch statusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge,
		http.StatusUnauthorized, http.StatusForbidden, http.StatusPaymentRequired:
		return ErrorClassRequest
	}
	return ErrorClassUpstream
}

// clientErrorMessageForClass 给出分级对应的兜底文案。调用方仍需优先透传上游
// 可识别信号;只有在上游没有可用正文时才用这里的文案。
func clientErrorMessageForClass(class ErrorClass, statusCode int) string {
	switch class {
	case ErrorClassRequest:
		if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
			return "请求未通过上游鉴权,请检查该模型/渠道权限,或联系管理员。"
		}
		if statusCode == http.StatusTooManyRequests {
			return "请求过于频繁或额度已用尽,请稍后重试或联系管理员。"
		}
		return "请求无法被上游接受,请调整请求内容;若持续失败请联系管理员。"
	case ErrorClassUnavailable:
		return "所有可用渠道当前均不可用,请稍后重试;若持续失败请联系管理员。"
	default:
		return "上游渠道暂时不可用,网关已尝试自动切换;请稍后重试。"
	}
}

// channelErrorMessageTemplate returns the channel-level error message template,
// or "" when the channel is nil.
func channelErrorMessageTemplate(ch *dbmodel.Channel) string {
	if ch == nil {
		return ""
	}
	return strings.TrimSpace(ch.ErrorMessageTemplate)
}

// applyChannelErrorMessageTemplate rewrites the final error message using the
// channel-level error_message_template. {upstream} is replaced with the upstream
// body. Returns ok=false when unset or when it must not override the upstream
// body.
//
// Red line: identifiable caller-side deterministic signals
// (context_length_exceeded / prompt is too long / content_filter ...) keep the
// raw upstream body so downstream clients can still trigger auto-compaction.
func applyChannelErrorMessageTemplate(template string, statusCode int, upstreamText string) (string, bool) {
	template = strings.TrimSpace(template)
	if template == "" {
		return "", false
	}
	if classifyErrorForClient(statusCode, upstreamText) == ErrorClassRequest {
		return "", false
	}
	message := strings.TrimSpace(strings.ReplaceAll(template, "{upstream}", strings.TrimSpace(upstreamText)))
	if message == "" {
		return "", false
	}
	return message, true
}

func parseCustomRetryableCodes(raw string) map[int]bool {
	result := make(map[int]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		code, err := strconv.Atoi(part)
		if err != nil || !validCustomErrorStatus(code) {
			continue
		}
		result[code] = true
	}
	return result
}

// getCustomRetryableCodes 读取自定义可重试码设置。读取失败或未配置时返回空集，
// 调用方保持默认分类行为。
func getCustomRetryableCodes() map[int]bool {
	raw, err := setting.GetString(dbmodel.SettingKeyCustomRetryableCodes)
	if err != nil || strings.TrimSpace(raw) == "" {
		return map[int]bool{}
	}
	return parseCustomRetryableCodes(raw)
}

// parseCustomErrorRules 解析自定义错误透传规则 JSON。空串返回空表；解析失败
// 或元素非法时返回空表（运行时不炸，行为回退到默认）。
func parseCustomErrorRules(raw string) []CustomErrorRule {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var rules []CustomErrorRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil
	}
	valid := rules[:0]
	for _, rule := range rules {
		rule.ChannelType = strings.ToLower(strings.TrimSpace(rule.ChannelType))
		rule.Keyword = strings.TrimSpace(rule.Keyword)
		rule.Message = strings.TrimSpace(rule.Message)
		if rule.Code == 0 && rule.Keyword == "" {
			continue
		}
		if rule.Code != 0 && !validCustomErrorStatus(rule.Code) {
			continue
		}
		if rule.CustomStatus != 0 && !validCustomErrorStatus(rule.CustomStatus) {
			continue
		}
		valid = append(valid, rule)
	}
	return valid
}

// getCustomErrorRules 读取自定义错误透传规则设置。
func getCustomErrorRules() []CustomErrorRule {
	raw, err := setting.GetString(dbmodel.SettingKeyCustomErrorRules)
	if err != nil {
		return nil
	}
	return parseCustomErrorRules(raw)
}

// matchCustomErrorRule 按“渠道类型 + 错误码/关键词”匹配第一条规则。
// upstreamText 是上游错误文本（已含状态码与响应体摘要），关键词做大小写
// 不敏感子串匹配。Code 与 Keyword 是 OR 关系：任一命中即算匹配；规则内
// ChannelType 非空时必须先命中渠道类型。
func matchCustomErrorRule(rules []CustomErrorRule, channelType outbound.OutboundType, statusCode int, upstreamText string) *CustomErrorRule {
	channelName := strings.ToLower(strings.TrimSpace(channelType.String()))
	loweredText := strings.ToLower(upstreamText)
	for i := range rules {
		rule := &rules[i]
		if rule.ChannelType != "" && rule.ChannelType != channelName {
			continue
		}
		matched := false
		if rule.Code != 0 && rule.Code == statusCode {
			matched = true
		}
		if !matched && rule.Keyword != "" && loweredText != "" && strings.Contains(loweredText, strings.ToLower(rule.Keyword)) {
			matched = true
		}
		if matched {
			return rule
		}
	}
	return nil
}

// customErrorIsDeterministicFailure 报告终止语义是否为确定性失败。
// 这类失败对同一请求重发必然得到同一结果，重试只会浪费资源；自定义重试
// 白名单不得覆盖它们（关键词不能凌驾于 TerminationCause 之上）。
func customErrorIsDeterministicFailure(termination model.TerminationMetadata) bool {
	switch termination.Cause {
	case model.TerminationCauseContextExhausted,
		model.TerminationCauseContentFilter,
		model.TerminationCauseRecitation,
		model.TerminationCausePromptBlocked,
		model.TerminationCauseRefusal:
		return true
	default:
		return false
	}
}

// terminalFailureError 把 TerminationCause 编码进 error 链，
// 让 attempt() 能用 errors.As 取出 Cause 做“确定性失败不重试”豁免判断。
// 它同时包装 errProviderTerminalFailure，保持 errors.Is 兼容。
type terminalFailureError struct {
	termination model.TerminationMetadata
}

func (e *terminalFailureError) Error() string {
	detail := strings.TrimSpace(e.termination.Detail)
	if detail == "" {
		detail = strings.TrimSpace(e.termination.ProviderReason)
	}
	if detail == "" {
		detail = string(e.termination.Cause)
	}
	if detail == "" {
		detail = "unknown"
	}
	return errProviderTerminalFailure.Error() + ": " + detail
}

func (e *terminalFailureError) Unwrap() error {
	return errProviderTerminalFailure
}

// terminalCauseFromError 从 error 链中提取 TerminationCause。
func terminalCauseFromError(err error) (model.TerminationMetadata, bool) {
	var terminalErr *terminalFailureError
	if errors.As(err, &terminalErr) {
		return terminalErr.termination, true
	}
	return model.TerminationMetadata{}, false
}

// resolveCustomErrorPresentation 根据命中规则计算最终呈现。
// 返回 (status, message, ok)：ok 为 false 表示未命中，保持默认呈现。
// message 为空表示调用方回退默认文案。
func resolveCustomErrorPresentation(rule *CustomErrorRule, upstreamStatus int, upstreamText string) (int, string, bool) {
	if rule == nil {
		return 0, "", false
	}
	status := rule.CustomStatus
	if rule.PassthroughStatus && validCustomErrorStatus(upstreamStatus) {
		status = upstreamStatus
	}
	if status == 0 {
		return 0, "", false
	}
	message := rule.Message
	if rule.PassthroughMessage {
		message = strings.TrimSpace(upstreamText)
	} else if strings.Contains(message, "{upstream}") {
		message = strings.ReplaceAll(message, "{upstream}", strings.TrimSpace(upstreamText))
	}
	return status, message, true
}

// writeCustomErrorPresentation 按规则呈现最终错误。
// message 为空时回退默认文案（http.StatusText），保证总有可用 body；
// 输出保持 OpenAI 兼容 {"error":{...}} 形状，不伪造成功。
func writeCustomErrorPresentation(c interface {
	Data(int, string, []byte)
	Abort()
}, status int, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = http.StatusText(status)
	}
	if message == "" {
		message = "request failed"
	}
	payload, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    clientErrorType(status),
			"code":    "",
			"param":   "",
		},
	})
	if err != nil {
		return
	}
	c.Data(status, "application/json", payload)
	c.Abort()
}
