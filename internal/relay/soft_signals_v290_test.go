package relay

import (
	"errors"
	"fmt"
	"testing"

	dbmodel "github.com/lingyuins/octopus/internal/model"
	transformerModel "github.com/lingyuins/octopus/internal/transformer/model"
)

// v2.9.0 软信号判据的单元测试：假流式指纹（含线上回放样本）、截断识别、
// 上游模型一致性、流中断哨兵与「空输出」判定。

// TestIsSuspectFakeStreamSignatureReplaySamples 用线上回放样本校准假流式判据：
//   - GG 假流式（首字 260.1s / 总 260.4s ≈ 99.9%）必须命中；
//   - 真流式长输出（首字占比 <22%）不得误伤；
//   - 时长 <60s 的短请求不参与判定。
func TestIsSuspectFakeStreamSignatureReplaySamples(t *testing.T) {
	cases := []struct {
		name                  string
		firstTokenMs, totalMs int64
		want                  bool
	}{
		{"GG fake-stream 260112/260379", 260112, 260379, true},
		{"GG fake-stream search 275000/275400", 275000, 275400, true},
		{"true streaming 8.4s head in 60s", 8400, 60000, false},
		{"true streaming 22% head", 22000, 100000, false},
		{"long head with 40%+ ratio", 61000, 150000, true},
		{"long head below 40% ratio", 61000, 200000, false},
		{"ratio exactly 60%", 60000, 100000, true},
		{"just below 60% and short head", 59999, 100000, false},
		{"short request not evaluated", 5000, 30000, false},
		{"zero inputs", 0, 0, false},
		{"negative input", -1, 100000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSuspectFakeStreamSignature(tc.firstTokenMs, tc.totalMs); got != tc.want {
				t.Fatalf("isSuspectFakeStreamSignature(%d,%d) = %v, want %v",
					tc.firstTokenMs, tc.totalMs, got, tc.want)
			}
		})
	}
}

// TestResponseHitLengthLimit 验证截断识别：finish_reason=length / max_tokens
// 命中，正常 stop 与 nil 响应不命中。
func TestResponseHitLengthLimit(t *testing.T) {
	t.Run("finish_reason length", func(t *testing.T) {
		resp := &transformerModel.InternalLLMResponse{
			Choices: []transformerModel.Choice{{FinishReason: strPtr("length")}},
		}
		if !responseHitLengthLimit(resp) {
			t.Fatal("finish_reason=length must be treated as token-limit termination")
		}
	})
	t.Run("finish_reason max_tokens", func(t *testing.T) {
		resp := &transformerModel.InternalLLMResponse{
			Choices: []transformerModel.Choice{{FinishReason: strPtr("max_tokens")}},
		}
		if !responseHitLengthLimit(resp) {
			t.Fatal("finish_reason=max_tokens must be treated as token-limit termination")
		}
	})
	t.Run("normal stop is not truncation", func(t *testing.T) {
		resp := &transformerModel.InternalLLMResponse{
			Choices: []transformerModel.Choice{{FinishReason: strPtr("stop")}},
		}
		if responseHitLengthLimit(resp) {
			t.Fatal("finish_reason=stop must not be treated as truncation")
		}
	})
	t.Run("nil response", func(t *testing.T) {
		if responseHitLengthLimit(nil) {
			t.Fatal("nil response must not be treated as truncation")
		}
	})
}

// TestUpstreamModelConsistency 验证上游自报模型的一致性口径：
// 大小写不敏感、容忍供应商前缀；真实不同名才算不一致；未回报不计入统计。
func TestUpstreamModelConsistency(t *testing.T) {
	cases := []struct {
		name      string
		upstream  string
		requested string
		reported  bool
		mismatch  bool
	}{
		{"exact match", "gemini-3.1-pro", "gemini-3.1-pro", true, false},
		{"case insensitive", "Gemini-3.1-Pro", "gemini-3.1-pro", true, false},
		{"vendor prefix tolerated", "google/gemini-3.1-pro", "gemini-3.1-pro", true, false},
		{"request prefix tolerated", "gemini-3.1-pro", "google/gemini-3.1-pro", true, false},
		{"real mismatch", "gpt-5-nano", "gemini-3.1-pro", true, true},
		{"upstream silent", "", "gemini-3.1-pro", false, false},
		{"empty requested model", "x", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &transformerModel.InternalLLMResponse{Model: tc.upstream}
			reported, mismatch := upstreamModelConsistency(resp, tc.requested)
			if reported != tc.reported || mismatch != tc.mismatch {
				t.Fatalf("upstreamModelConsistency(%q,%q) = (%v,%v), want (%v,%v)",
					tc.upstream, tc.requested, reported, mismatch, tc.reported, tc.mismatch)
			}
		})
	}
	if reported, mismatch := upstreamModelConsistency(nil, "m"); reported || mismatch {
		t.Fatalf("nil response = (%v,%v), want (false,false)", reported, mismatch)
	}
}

// TestIsMissingTerminalError 验证「流中断」哨兵识别：errors.Is 支持包装链。
func TestIsMissingTerminalError(t *testing.T) {
	if isMissingTerminalError(nil) {
		t.Fatal("nil error must not match")
	}
	if !isMissingTerminalError(errMissingStreamTerminal) {
		t.Fatal("sentinel must match")
	}
	if !isMissingTerminalError(fmt.Errorf("stream ended: %w", errMissingStreamTerminal)) {
		t.Fatal("wrapped sentinel must match via errors.Is")
	}
	if isMissingTerminalError(errors.New("unrelated failure")) {
		t.Fatal("unrelated error must not match")
	}
}

// TestResponseIsEffectivelyEmpty 验证「空输出」软信号判定：
//   - 有 message 对象但正文/工具调用全空（烁-gemini 空输出形态）必须判空
//     （回归：此前复用 token 估算文本，role 会被计入，导致永不判空）；
//   - reasoning-only 不算可见输出；
//   - 有正文 / 有输出 token / 内容过滤终态都不算空输出。
func TestResponseIsEffectivelyEmpty(t *testing.T) {
	t.Run("message object without visible content", func(t *testing.T) {
		metrics := &RelayMetrics{InternalResponse: &transformerModel.InternalLLMResponse{
			Choices: []transformerModel.Choice{{
				Message: &transformerModel.Message{Role: "assistant"},
			}},
		}}
		if !responseIsEffectivelyEmpty(metrics) {
			t.Fatal("success without visible output must be flagged as empty output")
		}
	})
	t.Run("reasoning only is not visible output", func(t *testing.T) {
		metrics := &RelayMetrics{InternalResponse: &transformerModel.InternalLLMResponse{
			Choices: []transformerModel.Choice{{
				Message: &transformerModel.Message{Role: "assistant", ReasoningContent: strPtr("thinking...")},
			}},
		}}
		if !responseIsEffectivelyEmpty(metrics) {
			t.Fatal("reasoning-only success must be flagged as empty output")
		}
	})
	t.Run("visible text is not empty", func(t *testing.T) {
		text := "好的，继续。"
		metrics := &RelayMetrics{InternalResponse: &transformerModel.InternalLLMResponse{
			Choices: []transformerModel.Choice{{
				Message: &transformerModel.Message{Role: "assistant", Content: transformerModel.MessageContent{Content: &text}},
			}},
		}}
		if responseIsEffectivelyEmpty(metrics) {
			t.Fatal("visible text must not be flagged as empty output")
		}
	})
	t.Run("reported output tokens is not empty", func(t *testing.T) {
		metrics := &RelayMetrics{Stats: dbmodel.StatsMetrics{OutputToken: 12}}
		if responseIsEffectivelyEmpty(metrics) {
			t.Fatal("reported output tokens must not be flagged as empty output")
		}
	})
	t.Run("provider refusal excluded", func(t *testing.T) {
		metrics := &RelayMetrics{InternalResponse: &transformerModel.InternalLLMResponse{
			Termination: transformerModel.TerminationMetadata{Cause: transformerModel.TerminationCauseContentFilter},
		}}
		if responseIsEffectivelyEmpty(metrics) {
			t.Fatal("provider refusal must not be flagged as channel empty output")
		}
	})
	t.Run("nil metrics", func(t *testing.T) {
		if responseIsEffectivelyEmpty(nil) {
			t.Fatal("nil metrics must not be flagged")
		}
	})
}
