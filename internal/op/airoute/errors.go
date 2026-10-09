package airoute

import (
	"net/http"
	"strconv"

	"github.com/lingyuins/octopus/internal/apperror"
)

// AI 路由用户可见错误的稳定码（apperror.Code，走 resp.ErrorWithAppError 透传）
// 与 i18n key（走 helper 的 AIRouteI18nError 断言写入 progress.MessageKey）。
// i18n key 与前端三份 locale 的 group.aiRoute.progress.runtime.* 同名 key 对应。
const (
	CodeAIRouteNoService         = "errors.aiRouteNoService"
	CodeAIRouteEmptyResult       = "errors.aiRouteEmptyResult"
	CodeAIRouteInvalidJSON       = "errors.aiRouteInvalidJSON"
	CodeAIRouteRateLimited       = "errors.aiRouteRateLimited"
	CodeAIRouteUpstreamTimeout   = "errors.aiRouteUpstreamTimeout"
	CodeAIRouteUnavailable       = "errors.aiRouteUnavailable"
	CodeAIRouteUpstreamStatus    = "errors.aiRouteUpstreamStatus"
	CodeAIRouteNotConfigured     = "errors.aiRouteNotConfigured"
	CodeAIRouteInvalidUpstreamID = "errors.aiRouteInvalidUpstreamID"

	I18nKeyAIRouteNoService       = "group.aiRoute.progress.runtime.noService"
	I18nKeyAIRouteEmptyResult     = "group.aiRoute.progress.runtime.emptyResult"
	I18nKeyAIRouteInvalidJSON     = "group.aiRoute.progress.runtime.invalidJSON"
	I18nKeyAIRouteRateLimited     = "group.aiRoute.progress.runtime.rateLimited"
	I18nKeyAIRouteUpstreamTimeout = "group.aiRoute.progress.runtime.upstreamTimeout"
	I18nKeyAIRouteUnavailable     = "group.aiRoute.progress.runtime.unavailable"
	I18nKeyAIRouteUpstreamStatus  = "group.aiRoute.progress.runtime.upstreamStatus"
)

// aiRouteError 同时携带 apperror 结构化信息（Code/Status/Params）与
// AIRouteI18nError 的 MessageKey/MessageArgs。helper 层用 errors.As 断言
// AIRouteI18nError 提取 i18n key；resp 层经 Unwrap 链仍可用 apperror.Code
// 等函数读取结构化字段。
type aiRouteError struct {
	appErr   *apperror.Error
	i18nKey  string
	i18nArgs map[string]any
}

// Error 实现 error 接口，转发内层 apperror 的消息。
func (e *aiRouteError) Error() string {
	if e == nil || e.appErr == nil {
		return ""
	}
	return e.appErr.Error()
}

func (e *aiRouteError) I18nMessageKey() string          { return e.i18nKey }
func (e *aiRouteError) I18nMessageArgs() map[string]any { return e.i18nArgs }

// Unwrap 让 errors.As 沿链找到内层 *apperror.Error。
func (e *aiRouteError) Unwrap() error { return e.appErr }

func newAIRouteError(code string, i18nKey string, message string, status int, params map[string]any) *aiRouteError {
	e := &aiRouteError{
		appErr:  apperror.New(code, message),
		i18nKey: i18nKey,
	}
	if status > 0 {
		e.appErr = e.appErr.WithStatus(status)
	}
	if len(params) > 0 {
		e.appErr = e.appErr.WithParams(params)
	}
	e.i18nArgs = params
	return e
}

func errAIRouteNoService(bucketIndex int) *aiRouteError {
	return newAIRouteError(CodeAIRouteNoService, I18nKeyAIRouteNoService,
		"No AI route analysis service available (batch "+strconv.Itoa(bucketIndex)+")",
		http.StatusServiceUnavailable, map[string]any{"batch_index": bucketIndex})
}

func errAIRouteEmptyResult() *aiRouteError {
	return newAIRouteError(CodeAIRouteEmptyResult, I18nKeyAIRouteEmptyResult,
		"AI returned an empty result",
		http.StatusBadGateway, nil)
}

func errAIRouteInvalidJSON() *aiRouteError {
	return newAIRouteError(CodeAIRouteInvalidJSON, I18nKeyAIRouteInvalidJSON,
		"AI response is not valid JSON",
		http.StatusBadGateway, nil)
}

func errAIRouteRateLimited(body string) *aiRouteError {
	msg := "AI analysis service rate limited, switching to another service"
	params := map[string]any{"body_suffix": ""}
	if body != "" {
		msg = msg + ": " + body
		params["body_suffix"] = ": " + body
	}
	return newAIRouteError(CodeAIRouteRateLimited, I18nKeyAIRouteRateLimited, msg, http.StatusTooManyRequests, params)
}

func errAIRouteUpstreamTimeout(body string) *aiRouteError {
	msg := "AI analysis service timed out; use a faster model or reduce the model list"
	params := map[string]any{"body_suffix": ""}
	if body != "" {
		msg = msg + ": " + body
		params["body_suffix"] = ": " + body
	}
	return newAIRouteError(CodeAIRouteUpstreamTimeout, I18nKeyAIRouteUpstreamTimeout, msg, http.StatusGatewayTimeout, params)
}

func errAIRouteUnavailable(body string) *aiRouteError {
	msg := "AI analysis service temporarily unavailable, please retry later"
	params := map[string]any{"body_suffix": ""}
	if body != "" {
		msg = msg + ": " + body
		params["body_suffix"] = ": " + body
	}
	return newAIRouteError(CodeAIRouteUnavailable, I18nKeyAIRouteUnavailable, msg, http.StatusBadGateway, params)
}

func errAIRouteUpstreamStatus(status int, body string) *aiRouteError {
	msg := "AI analysis failed: upstream status " + strconv.Itoa(status)
	params := map[string]any{"status": status, "body_suffix": ""}
	if body != "" {
		msg = msg + ": " + body
		params["body_suffix"] = ": " + body
	}
	return newAIRouteError(CodeAIRouteUpstreamStatus, I18nKeyAIRouteUpstreamStatus, msg, http.StatusBadGateway, params)
}

func errAIRouteNotConfigured() *aiRouteError {
	return newAIRouteError(CodeAIRouteNotConfigured, I18nKeyAIRouteNoService,
		"AI route service configuration is incomplete",
		http.StatusBadRequest, nil)
}

func errAIRouteInvalidUpstreamID(channelID int, model string) *aiRouteError {
	return newAIRouteError(CodeAIRouteInvalidUpstreamID, I18nKeyAIRouteEmptyResult,
		"AI returned a channel_id/upstream_model that does not exist: channel_id="+strconv.Itoa(channelID)+" model="+model,
		http.StatusBadRequest, map[string]any{
			"channel_id": channelID,
			"model":      model,
		})
}
