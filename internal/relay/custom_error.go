package relay

import (
	"encoding/json"
	"errors"
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

// parseCustomRetryableCodes 解析逗号分隔的自定义可重试状态码。空串返回空集；
// 非法片段直接丢弃（Validate 已在写入时拦截，运行时读到脏数据也不炸）。
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
