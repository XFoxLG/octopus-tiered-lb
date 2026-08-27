package relay

import (
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

func strPtr(s string) *string { return &s }

// --- isTruncatedOutput ---

func TestIsTruncatedOutput_LengthFinishReason(t *testing.T) {
	resp := &model.InternalLLMResponse{Choices: []model.Choice{{FinishReason: strPtr("length")}}}
	if !isTruncatedOutput(resp) {
		t.Fatalf("finish_reason=length should be detected as truncated")
	}
}

func TestIsTruncatedOutput_CaseInsensitive(t *testing.T) {
	resp := &model.InternalLLMResponse{Choices: []model.Choice{{FinishReason: strPtr("Length")}}}
	if !isTruncatedOutput(resp) {
		t.Fatalf("finish_reason=Length (any case) should be detected as truncated")
	}
}

func TestIsTruncatedOutput_StopNotTruncated(t *testing.T) {
	resp := &model.InternalLLMResponse{Choices: []model.Choice{{FinishReason: strPtr("stop")}}}
	if isTruncatedOutput(resp) {
		t.Fatalf("finish_reason=stop should not be truncated")
	}
}

func TestIsTruncatedOutput_NilFinishReason(t *testing.T) {
	resp := &model.InternalLLMResponse{Choices: []model.Choice{{}}}
	if isTruncatedOutput(resp) {
		t.Fatalf("nil finish_reason should not be truncated")
	}
}

func TestIsTruncatedOutput_NilResponse(t *testing.T) {
	if isTruncatedOutput(nil) {
		t.Fatalf("nil response should not be truncated")
	}
}

func TestIsTruncatedOutput_AnyChoiceTriggers(t *testing.T) {
	resp := &model.InternalLLMResponse{Choices: []model.Choice{
		{FinishReason: strPtr("stop")},
		{FinishReason: strPtr("length")},
	}}
	if !isTruncatedOutput(resp) {
		t.Fatalf("any choice with finish_reason=length should trigger truncation")
	}
}

func TestIsTruncatedOutput_EmptyContentStillTruncated(t *testing.T) {
	// 空内容 + length 结束：两条路径都可能关心，isTruncatedOutput 只看 finish_reason。
	// 调用方（relay.go）先判空输出再判截断，顺序保证互斥——这里只验证函数本身语义。
	resp := &model.InternalLLMResponse{Choices: []model.Choice{{FinishReason: strPtr("length")}}}
	if !isTruncatedOutput(resp) {
		t.Fatalf("length finish reason should be detected regardless of content")
	}
}
