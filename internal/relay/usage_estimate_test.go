package relay

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/relaylog"
	"github.com/lingyuins/octopus/internal/op/setting"
	transformerModel "github.com/lingyuins/octopus/internal/transformer/model"
	"github.com/lingyuins/octopus/internal/utils/tokenizer"
)

func estimateFixtureRequest() *transformerModel.InternalLLMRequest {
	return &transformerModel.InternalLLMRequest{
		Model: "gemini-3.1-pro",
		Messages: []transformerModel.Message{
			{Role: "system", Content: transformerModel.MessageContent{Content: strPtr("你是助手。")}},
			{Role: "user", Content: transformerModel.MessageContent{Content: strPtr("hello world, 你好世界")}},
		},
	}
}

func estimateFixtureResponse() *transformerModel.InternalLLMResponse {
	return &transformerModel.InternalLLMResponse{
		Choices: []transformerModel.Choice{
			{
				Index: 0,
				Message: &transformerModel.Message{
					Role:    "assistant",
					Content: transformerModel.MessageContent{Content: strPtr("OK")},
				},
			},
		},
	}
}

func newEstimateFixtureMetrics(t *testing.T) *RelayMetrics {
	t.Helper()
	metrics := NewRelayMetrics(1, "gemini-3.1-pro", "chat", "chat", "", "", estimateFixtureRequest())
	metrics.SetInternalResponse(estimateFixtureResponse(), "gemini-3.1-pro")
	return metrics
}

// TestApplyUsageEstimateTriggerMatrix 逐格验证 v2.8.13 的触发矩阵：
// 只有 reported/not_reported 且对应方向上报值 ≤0、可见文本非空时才写估算。
func TestApplyUsageEstimateTriggerMatrix(t *testing.T) {
	t.Run("reported_with_zero_completion_estimates_output_only", func(t *testing.T) {
		metrics := newEstimateFixtureMetrics(t)
		// 烁-gemini 样本形态：prompt>0（reported）但 completion 恒为 0。
		metrics.InternalResponse.Usage = &transformerModel.Usage{PromptTokens: 106_140}
		relayLog := model.RelayLog{UsageState: model.RelayLogUsageReported, InputTokens: 106_140, OutputTokens: 0}
		applyUsageEstimate(&relayLog, metrics, "gemini-3.1-pro")
		if relayLog.InputTokensEst != 0 {
			t.Fatalf("input must stay as reported, got est=%d", relayLog.InputTokensEst)
		}
		if relayLog.OutputTokensEst <= 0 {
			t.Fatalf("expected output estimate, got %+v", relayLog)
		}
	})

	t.Run("reported_with_reported_completion_skips", func(t *testing.T) {
		metrics := newEstimateFixtureMetrics(t)
		metrics.InternalResponse.Usage = &transformerModel.Usage{PromptTokens: 1000, CompletionTokens: 200}
		relayLog := model.RelayLog{UsageState: model.RelayLogUsageReported, InputTokens: 1000, OutputTokens: 200}
		applyUsageEstimate(&relayLog, metrics, "gemini-3.1-pro")
		if relayLog.InputTokensEst != 0 || relayLog.OutputTokensEst != 0 {
			t.Fatalf("reported usage must not be estimated: %+v", relayLog)
		}
	})

	t.Run("not_reported_estimates_both_directions", func(t *testing.T) {
		metrics := newEstimateFixtureMetrics(t)
		relayLog := model.RelayLog{UsageState: model.RelayLogUsageNotReported}
		applyUsageEstimate(&relayLog, metrics, "gemini-3.1-pro")
		if relayLog.InputTokensEst <= 0 || relayLog.OutputTokensEst <= 0 {
			t.Fatalf("expected both estimates, got input=%d output=%d", relayLog.InputTokensEst, relayLog.OutputTokensEst)
		}
	})

	t.Run("not_reported_with_empty_text_skips", func(t *testing.T) {
		metrics := NewRelayMetrics(1, "gemini-3.1-pro", "chat", "chat", "", "", &transformerModel.InternalLLMRequest{})
		metrics.SetInternalResponse(&transformerModel.InternalLLMResponse{}, "gemini-3.1-pro")
		relayLog := model.RelayLog{UsageState: model.RelayLogUsageNotReported}
		applyUsageEstimate(&relayLog, metrics, "gemini-3.1-pro")
		if relayLog.InputTokensEst != 0 || relayLog.OutputTokensEst != 0 {
			t.Fatalf("empty text must not produce estimates: %+v", relayLog)
		}
	})

	for _, usageState := range []string{
		model.RelayLogUsageClientDisconnect,
		model.RelayLogUsageMissingTerminal,
		model.RelayLogUsageFailedNoResponse,
		model.RelayLogUsageEmptyOutput,
		model.RelayLogUsageNotApplicable,
		"",
	} {
		t.Run("skips_"+usageState, func(t *testing.T) {
			metrics := newEstimateFixtureMetrics(t)
			relayLog := model.RelayLog{UsageState: usageState}
			applyUsageEstimate(&relayLog, metrics, "gemini-3.1-pro")
			if relayLog.InputTokensEst != 0 || relayLog.OutputTokensEst != 0 {
				t.Fatalf("usage_state %q must not estimate: %+v", usageState, relayLog)
			}
		})
	}

	t.Run("nil_inputs_are_safe", func(t *testing.T) {
		relayLog := model.RelayLog{UsageState: model.RelayLogUsageNotReported}
		applyUsageEstimate(&relayLog, &RelayMetrics{}, "gpt-4o")
		if relayLog.InputTokensEst != 0 || relayLog.OutputTokensEst != 0 {
			t.Fatalf("nil request/response must not estimate: %+v", relayLog)
		}
		applyUsageEstimate(nil, nil, "")
	})
}

// TestEstimateTextExtraction 验证输入/输出文本提取口径：
// 输入只取消息可见文本（忽略图片/附件/tools 定义）；输出取正文 + tool_call 参数，
// 不含思考内容。
func TestEstimateTextExtraction(t *testing.T) {
	request := &transformerModel.InternalLLMRequest{
		Messages: []transformerModel.Message{
			{
				Role: "user",
				Content: transformerModel.MessageContent{
					MultipleContent: []transformerModel.MessageContentPart{
						{Type: "text", Text: strPtr("第一段")},
						{Type: "image_url", ImageURL: &transformerModel.ImageURL{URL: "data:image/png;base64,AAAA"}},
						{Type: "text", Text: strPtr("第二段")},
					},
				},
			},
		},
	}
	requestText := requestEstimateText(request)
	for _, want := range []string{"user", "第一段", "第二段"} {
		if !strings.Contains(requestText, want) {
			t.Fatalf("request text %q is missing %q", requestText, want)
		}
	}
	if strings.Contains(requestText, "base64") {
		t.Fatalf("image payloads must not be estimated: %q", requestText)
	}

	response := &transformerModel.InternalLLMResponse{
		Choices: []transformerModel.Choice{
			{
				Message: &transformerModel.Message{
					Role:             "assistant",
					Content:          transformerModel.MessageContent{Content: strPtr("visible answer")},
					ReasoningContent: strPtr("hidden chain of thought"),
					ToolCalls: []transformerModel.ToolCall{
						{Function: transformerModel.FunctionCall{Name: "lookup", Arguments: `{"q":"x"}`}},
					},
				},
			},
		},
	}
	responseText := responseEstimateText(response)
	if !strings.Contains(responseText, "visible answer") || !strings.Contains(responseText, `{"q":"x"}`) {
		t.Fatalf("response text must include visible content and tool arguments, got %q", responseText)
	}
	if strings.Contains(responseText, "hidden chain of thought") {
		t.Fatalf("reasoning text must not be counted as output tokens: %q", responseText)
	}
}

func TestEstimateSampleSegmentsAreRuneAligned(t *testing.T) {
	text := strings.Repeat("a你好", 1000) // 9 bytes per unit → 9000 bytes of valid UTF-8
	cfg := usageEstimateSampleConfig{thresholdBytes: 100, segments: 5, segmentBytes: 64}
	segments := estimateSampleSegments(text, cfg)
	if len(segments) != cfg.segments {
		t.Fatalf("segment count = %d, want %d", len(segments), cfg.segments)
	}
	if !strings.HasPrefix(text, segments[0]) {
		t.Fatalf("first segment must start at the head: %q", segments[0])
	}
	if !strings.HasSuffix(text, segments[len(segments)-1]) {
		t.Fatalf("last segment must end at the tail: %q", segments[len(segments)-1])
	}
	for index, segment := range segments {
		if segment == "" {
			t.Fatalf("segment %d is empty", index)
		}
		if !utf8.ValidString(segment) {
			t.Fatalf("segment %d is not valid UTF-8", index)
		}
		if len(segment) > cfg.segmentBytes {
			t.Fatalf("segment %d exceeds budget: %d bytes", index, len(segment))
		}
	}
}

func TestExtrapolateSampleTokens(t *testing.T) {
	cases := []struct {
		sample, sampled, total, want int
	}{
		{100, 1000, 10000, 1000},
		{1, 3, 10, 3},
		{2, 3, 10, 7},
		{100, 10000, 5000, 100}, // 采样已覆盖全文：直接返回样本值
		{0, 100, 1000, 0},
		{10, 0, 1000, 0},
		{10, 100, 0, 0},
	}
	for _, tc := range cases {
		if got := extrapolateSampleTokens(tc.sample, tc.sampled, tc.total); got != tc.want {
			t.Fatalf("extrapolateSampleTokens(%d, %d, %d) = %d, want %d", tc.sample, tc.sampled, tc.total, got, tc.want)
		}
	}
}

// TestEstimateTextTokensSamplesAboveThreshold 用均匀重复的中文字符验证采样外推：
// 样本与全文的 token 密度一致，外推值应与全量计数接近（±20%）。
func TestEstimateTextTokensSamplesAboveThreshold(t *testing.T) {
	text := strings.Repeat("字", 5000) // 15000 bytes
	full := tokenizer.CountTokens(text, "gpt-4o")
	if full <= 0 {
		t.Fatalf("baseline full count = %d", full)
	}
	sampled := estimateTextTokens(text, "gpt-4o", usageEstimateSampleConfig{
		thresholdBytes: 1000,
		segments:       8,
		segmentBytes:   500,
	})
	if sampled <= 0 {
		t.Fatalf("sampled estimate = %d", sampled)
	}
	if deviation := math.Abs(float64(sampled-full) / float64(full)); deviation > 0.2 {
		t.Fatalf("sampled estimate %d deviates from full count %d by %.0f%%", sampled, full, deviation*100)
	}

	short := "hello world"
	if got := estimateTextTokens(short, "gpt-4o", defaultUsageEstimateSampleConfig); got != tokenizer.CountTokens(short, "gpt-4o") {
		t.Fatalf("short text must be counted in full: got %d", got)
	}
}

// TestSaveLogWritesUsageEstimateFields 端到端验证 saveLog 接线：reported +
// completion=0 的漏报渠道在日志里写入输出估算，同时保留上报原值。
func TestSaveLogWritesUsageEstimateFields(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "metrics-estimate.db")
	if err := db.InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	if err := db.InitLogDB("", "", false); err != nil {
		t.Fatalf("InitLogDB(shared) failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := setting.RefreshCache(context.Background()); err != nil {
		t.Fatalf("RefreshCache failed: %v", err)
	}
	t.Cleanup(relaylog.SetCacheForTest(nil))

	metrics := newEstimateFixtureMetrics(t)
	// saveLog 的 InputTokens/OutputTokens 取自 SetInternalResponse 从 usage 派生的
	// Stats（真实链路同此），因此要在写入 Usage 后再走一次 SetInternalResponse。
	reported := estimateFixtureResponse()
	reported.Usage = &transformerModel.Usage{PromptTokens: 106_140}
	metrics.SetInternalResponse(reported, "gemini-3.1-pro")
	metrics.saveLog(context.Background(), nil, 9*time.Second, []model.ChannelAttempt{
		{ChannelID: 330, ChannelName: "shuo-gemini", ModelName: "gemini-3.1-pro", Status: model.AttemptSuccess},
	}, 330, "shuo-gemini")

	logs, _ := relaylog.GetCacheAndLock()
	if len(logs) == 0 {
		t.Fatal("expected a log row in cache")
	}
	last := logs[len(logs)-1]
	if last.UsageState != model.RelayLogUsageReported {
		t.Fatalf("usage_state = %q, want reported", last.UsageState)
	}
	if last.InputTokens != 106_140 {
		t.Fatalf("reported input must be preserved: %d", last.InputTokens)
	}
	if last.InputTokensEst != 0 {
		t.Fatalf("reported input direction must not be estimated: %d", last.InputTokensEst)
	}
	if last.OutputTokensEst <= 0 {
		t.Fatalf("expected estimated output tokens, got %+v", last)
	}
}
