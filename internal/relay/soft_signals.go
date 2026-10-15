package relay

import (
	"errors"
	"strings"

	"github.com/lingyuins/octopus/internal/relay/balancer"
	transformerModel "github.com/lingyuins/octopus/internal/transformer/model"
)

// v2.9.0：把「成功但可疑」的行为变成可统计的软信号，进入 Auto 打分。
//
// 与 web/src/lib/fake-stream.ts 同源的假流式判据（服务端复算版）：
//   - 只对时长 ≥60s 的请求生效（小请求首字占比天然偏高，没有诊断价值）；
//   - 首字占比 ≥60%，或首字 ≥60s 且占比 ≥40%。
//
// 与服务端旧判据的差别：这里使用「本次成功尝试」的时长与首字，
// 天然排除了此前失败重试链的耗时（前端 v2.8.11/v2.8.12 的边界修补在这里
// 不再需要——attempt span 本身就是产出内容的那一次尝试）。
const (
	softFakeStreamMinTotalMs    = 60_000
	softFakeStreamMinRatio      = 0.6
	softFakeStreamLongHeadMs    = 60_000
	softFakeStreamLongHeadRatio = 0.4
)

// isSuspectFakeStreamSignature 判定「首个可见内容出现得太晚」的伪流式指纹。
// 任一侧缺失（≤0）返回 false，不产生信号。
func isSuspectFakeStreamSignature(firstTokenMs, totalMs int64) bool {
	if firstTokenMs <= 0 || totalMs <= 0 {
		return false
	}
	if totalMs < softFakeStreamMinTotalMs {
		return false
	}
	ratio := float64(firstTokenMs) / float64(totalMs)
	if ratio >= softFakeStreamMinRatio {
		return true
	}
	return firstTokenMs >= softFakeStreamLongHeadMs && ratio >= softFakeStreamLongHeadRatio
}

// recordSuccessSoftSignals 在成功尝试上采集软信号并写入 Auto 统计。
// 只在真正产出给客户端的成功响应上调用（attempt() 的成功分支）。
func recordSuccessSoftSignals(ra *relayAttempt, span *balancer.AttemptSpan) {
	if ra == nil || ra.metrics == nil || span == nil {
		return
	}
	metrics := ra.metrics
	channelID := ra.channel.ID
	modelName := ra.internalRequest.Model
	if strings.TrimSpace(modelName) == "" {
		modelName = ra.requestModel
	}

	// 1) 假流式：首个可见内容出现在本次尝试的大部分时长之后。
	firstVisible := metrics.FirstTokenTime
	if firstVisible.IsZero() {
		firstVisible = metrics.UpstreamFirstVisibleTime
	}
	if !firstVisible.IsZero() {
		firstTokenMs := firstVisible.Sub(span.StartedAt()).Milliseconds()
		if isSuspectFakeStreamSignature(firstTokenMs, span.Duration().Milliseconds()) {
			balancer.RecordAutoSoftSignal(channelID, modelName, balancer.SoftSignalFakeStream)
		}
	}

	// 2) 空输出：成功但既没有上报输出 token，也没有任何可见文本。
	//    （上游自报 completion=0 但正文存在的情况属于「输出量未计」，不是软信号。）
	if responseIsEffectivelyEmpty(metrics) {
		balancer.RecordAutoSoftSignal(channelID, modelName, balancer.SoftSignalEmptyOutput)
	}

	// 3) 截断：上游正常结束但触达长度上限（provider_termination_reason=length）。
	if responseHitLengthLimit(metrics.InternalResponse) {
		balancer.RecordAutoSoftSignal(channelID, modelName, balancer.SoftSignalTruncated)
	}

	// 4) 上游自报模型一致性（GPT-Load rc.34/#412 同类能力）：只统计自报样本。
	if reported, mismatch := upstreamModelConsistency(metrics.InternalResponse, modelName); reported {
		balancer.RecordAutoUpstreamModel(channelID, modelName, true, mismatch)
	}
}

// recordMissingTerminalSoftSignal 记录一次「上游流未发终止事件」（软信号）。
// 这类失败在现有语义里走 ScopeSameChannel，不计入渠道失败统计（shouldRecordChannelFailure
// 只认 NextChannel/AbortAll），因此需要在这里单独留痕，否则对 Auto 完全不可见。
func recordMissingTerminalSoftSignal(channelID int, modelName string, err error) {
	if !isMissingTerminalError(err) {
		return
	}
	balancer.RecordAutoSoftSignal(channelID, modelName, balancer.SoftSignalMissingTerminal)
}

func isMissingTerminalError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errMissingStreamTerminal)
}

// responseIsEffectivelyEmpty 判定成功响应是否「没有任何可见输出」。
// 内容过滤类终态（content_filter/refusal 等）由上游主动拒绝输出，不算渠道软信号。
//
// 注意：这里不能复用 responseEstimateText——那是 token 估算口径，会把 message.role
// 也计入文本，导致「有 message 对象但正文为空」被误判为非空输出，空输出软信号
// 永远不会命中；可见性判定统一走 messageHasVisibleContent（reasoning 不算可见）。
func responseIsEffectivelyEmpty(metrics *RelayMetrics) bool {
	if metrics == nil {
		return false
	}
	if metrics.Stats.OutputToken > 0 {
		return false
	}
	if termination := responseTermination(metrics.InternalResponse); termination.HasCause() {
		if termination.Cause.IsProviderRefusal() {
			return false
		}
	}
	return !responseHasVisibleContent(metrics.InternalResponse)
}

// responseHasVisibleContent 判定响应（Message 或 Delta）是否包含对客户端可见的
// 内容（正文、多模态、工具调用、音频）。
func responseHasVisibleContent(resp *transformerModel.InternalLLMResponse) bool {
	if resp == nil {
		return false
	}
	for i := range resp.Choices {
		choice := &resp.Choices[i]
		if messageHasVisibleContent(choice.Message) || messageHasVisibleContent(choice.Delta) {
			return true
		}
	}
	return false
}

// responseHitLengthLimit 判定响应是否触达长度上限（finish_reason=length）。
func responseHitLengthLimit(resp *transformerModel.InternalLLMResponse) bool {
	if resp == nil {
		return false
	}
	termination := responseTermination(resp)
	if termination.Cause == transformerModel.TerminationCauseTokenLimit {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(termination.ProviderReason), "length") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(termination.ProviderReason), "max_tokens") {
		return true
	}
	for i := range resp.Choices {
		choiceTermination := terminationForChoice(resp.Choices[i])
		if choiceTermination.Cause == transformerModel.TerminationCauseTokenLimit {
			return true
		}
		if reason := providerReasonForChoice(resp.Choices[i]); reason != "" {
			if strings.EqualFold(reason, "length") || strings.EqualFold(reason, "max_tokens") {
				return true
			}
		}
	}
	return false
}

func providerReasonForChoice(choice transformerModel.Choice) string {
	// terminationForChoice 已处理「结构化 Termination 优先、FinishReason 兜底」
	// 的推导，这里直接复用，避免两处口径漂移。
	return strings.TrimSpace(terminationForChoice(choice).ProviderReason)
}

// responseTermination 提取响应的终态分类（响应级优先，其次第一个有终态的 choice）。
func responseTermination(resp *transformerModel.InternalLLMResponse) transformerModel.TerminationMetadata {
	if resp == nil {
		return transformerModel.TerminationMetadata{}
	}
	termination := resp.Termination
	if termination.HasCause() {
		return termination
	}
	for i := range resp.Choices {
		if choiceTermination := terminationForChoice(resp.Choices[i]); choiceTermination.HasCause() {
			return choiceTermination
		}
	}
	return termination
}

// upstreamModelConsistency 返回上游自报模型是否与请求模型一致。
// reported=false 表示上游没有回报模型（或只回报了相同别名无法判断），不计入统计。
func upstreamModelConsistency(resp *transformerModel.InternalLLMResponse, requestedModel string) (reported bool, mismatch bool) {
	if resp == nil {
		return false, false
	}
	upstream := strings.TrimSpace(resp.Model)
	if upstream == "" {
		return false, false
	}
	requested := strings.TrimSpace(requestedModel)
	if requested == "" {
		return false, false
	}
	if strings.EqualFold(upstream, requested) {
		return true, false
	}
	// 上游常见写法带供应商前缀（如 "google/gemini-3.1-pro"）或日期后缀快照；
	// 只要结尾同名就不算不一致，避免把别名误报成「模型不一致」。
	upstreamLower := strings.ToLower(upstream)
	requestedLower := strings.ToLower(requested)
	if strings.HasSuffix(upstreamLower, "/"+requestedLower) || strings.HasSuffix(requestedLower, "/"+upstreamLower) {
		return true, false
	}
	return true, true
}
