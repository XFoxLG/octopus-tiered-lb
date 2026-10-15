package relay

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/channelmodellimit"
	"github.com/lingyuins/octopus/internal/op/setting"
)

// v2.9.0：请求前上下文预检与「渠道×模型」上下文上限。
//
// 动机（2026-10-14 实测基线）：gpt-5-nano 分组混入 32K 上下文渠道时，65K/110K
// prompt 被随机抽中即上游 400（maximum context length is 32768）；路由器对
// 「渠道×模型能力」完全无感知。这里把已知上限变成候选闸门，并把接近上限
// （≥阈值，默认 90%）的请求写一条 warning 便于调查；不改写、不截断请求。
type contextPrecheckPlan struct {
	enabled        bool
	warnThreshold  float64
	estimatedInput int
	warned         bool
}

// prepareContextPrecheck 读取开关/阈值并估算输入 token（每请求只算一次）。
func prepareContextPrecheck(req *relayRequest) *contextPrecheckPlan {
	plan := &contextPrecheckPlan{enabled: true, warnThreshold: 0.9}
	if req == nil || req.internalRequest == nil {
		plan.enabled = false
		return plan
	}
	if v, err := setting.GetBool(model.SettingKeyRelayContextPrecheckEnabled); err == nil {
		plan.enabled = v
	}
	if v, err := setting.GetInt(model.SettingKeyRelayContextWarnThreshold); err == nil && v >= 1 && v <= 100 {
		plan.warnThreshold = float64(v) / 100.0
	}
	if !plan.enabled {
		return plan
	}
	if text := requestEstimateText(req.internalRequest); text != "" {
		plan.estimatedInput = estimateTextTokens(text, req.internalRequest.Model, defaultUsageEstimateSampleConfig)
	}
	return plan
}

// evaluate 评估单个候选：over=true 表示估算输入超过该渠道×模型的已知上限，
// 必须从候选中排除；warn=true 表示已到告警阈值但照常路由。
func (p *contextPrecheckPlan) evaluate(channelID int, resolvedModel string) (over bool, limit int, source string, warn bool) {
	if p == nil || !p.enabled || p.estimatedInput <= 0 {
		return false, 0, "", false
	}
	entry, ok := channelmodellimit.Lookup(channelID, resolvedModel)
	if !ok || entry.MaxContextTokens <= 0 {
		return false, 0, "", false
	}
	if p.estimatedInput > entry.MaxContextTokens {
		return true, entry.MaxContextTokens, entry.Source, false
	}
	if float64(p.estimatedInput) >= float64(entry.MaxContextTokens)*p.warnThreshold {
		return false, entry.MaxContextTokens, entry.Source, true
	}
	return false, entry.MaxContextTokens, entry.Source, false
}

// contextOverflowMarkers 是上游「输入超过上下文上限」错误的识别标记。
// 与 custom_error.go 的请求方语义标记同源，这里只取上下文类（不吞内容过滤等）。
var contextOverflowMarkers = []string{
	"context_length_exceeded",
	"context length exceeded",
	"maximum context length",
	"prompt is too long",
	"too many tokens",
	"context window",
	"input exceeds",
}

// isContextOverflowError 判定一次确定性失败是否为「输入超过上游上下文上限」。
func isContextOverflowError(statusCode int, err error) bool {
	if err == nil {
		return false
	}
	switch statusCode {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
	default:
		return false
	}
	lowered := strings.ToLower(err.Error())
	for _, marker := range contextOverflowMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// maybeRecordContextOverflow 在确定性超限终态上累计学习证据；写入 learned 上限时
// 补一条 warning 级错误日志，便于线上核对「学到了什么、从哪次请求学到」。
func maybeRecordContextOverflow(ctx context.Context, plan *contextPrecheckPlan, channelID int, resolvedModel string, statusCode int, err error) {
	if plan == nil || !plan.enabled || plan.estimatedInput <= 0 {
		return
	}
	if !isContextOverflowError(statusCode, err) {
		return
	}
	written, evidence := channelmodellimit.RecordOverflow(ctx, channelID, resolvedModel, plan.estimatedInput)
	if written {
		writeRelayErrorLog(nil, relayErrorLogLevelWarning, fmt.Sprintf(
			"channel model context limit learned: channel_id=%d model=%s limit=%d evidence=%d",
			channelID, resolvedModel, plan.estimatedInput, evidence))
	}
}
