package relay

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lingyuins/octopus/internal/model"
	transformerModel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/utils/tokenizer"
)

// v2.8.13：上游漏报用量时的日志级 Token 估算。
//
// 估算只做展示补充：当 usage_state 为 reported/not_reported、某个方向的上报值
// ≤0、且对应可见文本非空时，按 tiktoken 计数写入 *_tokens_est。估算值不参与
// 计费、不参与统计聚合、不改变 usage_state；前端以「≈N（估算）」呈现。
const (
	// usageEstimateSampleThresholdBytes 文本超过该 UTF-8 字节数时切换为
	// 等距采样外推，给单次估算的计数成本设一个上界。
	usageEstimateSampleThresholdBytes = 128 << 10
	// usageEstimateSampleSegments 等距采样段数。
	usageEstimateSampleSegments = 16
	// usageEstimateSampleSegmentBytes 每段最多取该 UTF-8 字节数（按 rune 对齐）。
	usageEstimateSampleSegmentBytes = 8 << 10
	// usageEstimateSlowWarnThreshold 单次估算耗时超过它时写一条 warning 错误日志，
	// 便于上线后直观看到异常慢的估算（阈值/文本规模的校准依据）。
	usageEstimateSlowWarnThreshold = 2 * time.Second
)

type usageEstimateSampleConfig struct {
	thresholdBytes int
	segments       int
	segmentBytes   int
}

var defaultUsageEstimateSampleConfig = usageEstimateSampleConfig{
	thresholdBytes: usageEstimateSampleThresholdBytes,
	segments:       usageEstimateSampleSegments,
	segmentBytes:   usageEstimateSampleSegmentBytes,
}

// applyUsageEstimate 按触发矩阵填充 relayLog 的输入/输出估算字段：
//   - usage_state ∈ {reported, not_reported}（其余成因：内容不完整或没有 Token
//     概念）并且对应方向上报值 ≤0、可见文本非空时才估算；
//   - reported 下输入必然 >0，因此实际只会补输出方向（completion=0 的漏报渠道）；
//   - 只补 0，不校正虚高上报。
func applyUsageEstimate(relayLog *model.RelayLog, m *RelayMetrics, actualModel string) {
	if relayLog == nil || m == nil {
		return
	}
	switch relayLog.UsageState {
	case model.RelayLogUsageReported, model.RelayLogUsageNotReported:
	default:
		return
	}
	if relayLog.InputTokens <= 0 {
		if text := requestEstimateText(m.InternalRequest); text != "" {
			relayLog.InputTokensEst = estimateTextTokensForLog(text, actualModel, "input", m)
		}
	}
	if relayLog.OutputTokens <= 0 {
		if text := responseEstimateText(m.InternalResponse); text != "" {
			relayLog.OutputTokensEst = estimateTextTokensForLog(text, actualModel, "output", m)
		}
	}
}

func estimateTextTokensForLog(text, modelName, direction string, m *RelayMetrics) int {
	started := time.Now()
	tokens := estimateTextTokens(text, modelName, defaultUsageEstimateSampleConfig)
	if elapsed := time.Since(started); elapsed >= usageEstimateSlowWarnThreshold {
		writeRelayErrorLog(nil, relayErrorLogLevelWarning, fmt.Sprintf(
			"relay token estimate slow: direction=%s model=%s bytes=%d tokens=%d elapsed=%s trace_id=%s",
			direction, modelName, len(text), tokens, elapsed.Round(time.Millisecond), relayTraceID(m),
		))
	}
	return tokens
}

func relayTraceID(m *RelayMetrics) string {
	if m == nil || m.RequestTrace == nil {
		return ""
	}
	return m.RequestTrace.id
}

// requestEstimateText 渲染输入估算用的提示文本：只含会话消息的可见文本
// （带 role 轻量标记），忽略图片/音频/附件与 tools 定义。
func requestEstimateText(req *transformerModel.InternalLLMRequest) string {
	if req == nil || len(req.Messages) == 0 {
		return ""
	}
	var builder strings.Builder
	for i := range req.Messages {
		writeMessageEstimateText(&builder, &req.Messages[i], false)
	}
	return builder.String()
}

// responseEstimateText 渲染输出估算用的可见文本：正文 + tool_call 参数，
// 不含思考内容（reasoning 继续由「思考 N 字」单独展示）与图片/附件。
func responseEstimateText(resp *transformerModel.InternalLLMResponse) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	var builder strings.Builder
	for i := range resp.Choices {
		choice := &resp.Choices[i]
		writeMessageEstimateText(&builder, choice.Message, true)
		if choice.Delta != nil {
			writeMessageEstimateText(&builder, choice.Delta, true)
		}
	}
	return builder.String()
}

func writeMessageEstimateText(builder *strings.Builder, msg *transformerModel.Message, includeToolCalls bool) {
	if msg == nil {
		return
	}
	if role := strings.TrimSpace(msg.Role); role != "" {
		builder.WriteString(role)
		builder.WriteByte('\n')
	}
	if msg.Content.Content != nil {
		if content := *msg.Content.Content; content != "" {
			builder.WriteString(content)
			builder.WriteByte('\n')
		}
	}
	for i := range msg.Content.MultipleContent {
		part := &msg.Content.MultipleContent[i]
		if part.Type != "text" || part.Text == nil {
			continue
		}
		if text := *part.Text; text != "" {
			builder.WriteString(text)
			builder.WriteByte('\n')
		}
	}
	if !includeToolCalls {
		return
	}
	for i := range msg.ToolCalls {
		if arguments := strings.TrimSpace(msg.ToolCalls[i].Function.Arguments); arguments != "" {
			builder.WriteString(arguments)
			builder.WriteByte('\n')
		}
	}
}

// estimateTextTokens 返回文本的估算 Token 数：小文本全量计数；超过阈值时
// 等距采样外推（样本 token × 总字节 / 样本字节，四舍五入）。
func estimateTextTokens(text, modelName string, cfg usageEstimateSampleConfig) int {
	totalBytes := len(text)
	if totalBytes == 0 {
		return 0
	}
	if cfg.thresholdBytes <= 0 || totalBytes <= cfg.thresholdBytes ||
		cfg.segments < 2 || cfg.segmentBytes <= 0 {
		return tokenizer.CountTokens(text, modelName)
	}
	sampleTokens := 0
	sampledBytes := 0
	for _, segment := range estimateSampleSegments(text, cfg) {
		sampledBytes += len(segment)
		sampleTokens += tokenizer.CountTokens(segment, modelName)
	}
	return extrapolateSampleTokens(sampleTokens, sampledBytes, totalBytes)
}

func extrapolateSampleTokens(sampleTokens, sampledBytes, totalBytes int) int {
	if sampleTokens <= 0 || sampledBytes <= 0 || totalBytes <= 0 {
		return 0
	}
	if sampledBytes >= totalBytes {
		return sampleTokens
	}
	return int(math.Round(float64(sampleTokens) * float64(totalBytes) / float64(sampledBytes)))
}

// estimateSampleSegments 在文本上均匀取 cfg.segments 个窗口：首窗贴头部、
// 末窗贴尾部，每窗最多 cfg.segmentBytes 字节，并按 rune 边界对齐，保证每个
// 样本都是合法 UTF-8、不会从字符中间切开。
func estimateSampleSegments(text string, cfg usageEstimateSampleConfig) []string {
	total := len(text)
	segmentBytes := cfg.segmentBytes
	if segmentBytes > total {
		segmentBytes = total
	}
	span := total - segmentBytes
	segments := make([]string, 0, cfg.segments)
	for i := 0; i < cfg.segments; i++ {
		start := 0
		if cfg.segments > 1 {
			start = span * i / (cfg.segments - 1)
		}
		start = alignRuneStart(text, start)
		end := start + segmentBytes
		if end > total {
			end = total
		}
		end = alignRuneEnd(text, start, end)
		if end <= start {
			continue
		}
		segments = append(segments, text[start:end])
	}
	return segments
}

func alignRuneStart(text string, pos int) int {
	if pos <= 0 {
		return 0
	}
	if pos >= len(text) {
		return len(text)
	}
	for pos < len(text) && !utf8.RuneStart(text[pos]) {
		pos++
	}
	return pos
}

func alignRuneEnd(text string, start, end int) int {
	if end >= len(text) {
		return len(text)
	}
	for end > start && !utf8.RuneStart(text[end]) {
		end--
	}
	return end
}
