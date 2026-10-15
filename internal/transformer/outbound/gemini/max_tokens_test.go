package gemini

import (
	"testing"

	"github.com/lingyuins/octopus/internal/transformer/model"
)

// v2.8.15 协议层 fix-p1：responses 入站的 max_output_tokens 落在
// MaxCompletionTokens；gemini 出站此前只读 MaxTokens，导致输出上限丢失
// （对拍 diff：/generationConfig/maxOutputTokens|only_oracle）。
func TestConvertMaxTokensFallsBackToMaxCompletionTokens(t *testing.T) {
	limit := int64(256)
	req := &model.InternalLLMRequest{
		Model:               "gemini-3.1-pro-preview",
		MaxCompletionTokens: &limit,
	}

	got := convertLLMToGeminiRequest(req)
	if got.GenerationConfig == nil || got.GenerationConfig.MaxOutputTokens != 256 {
		t.Fatalf("generationConfig = %+v, want maxOutputTokens=256", got.GenerationConfig)
	}
}

func TestConvertMaxTokensPrefersMaxTokensWhenBothSet(t *testing.T) {
	maxTokens := int64(64)
	maxCompletion := int64(512)
	req := &model.InternalLLMRequest{
		Model:               "gemini-3.1-pro-preview",
		MaxTokens:           &maxTokens,
		MaxCompletionTokens: &maxCompletion,
	}

	got := convertLLMToGeminiRequest(req)
	if got.GenerationConfig == nil || got.GenerationConfig.MaxOutputTokens != 64 {
		t.Fatalf("generationConfig = %+v, want maxOutputTokens=64（MaxTokens 优先）", got.GenerationConfig)
	}
}

func TestConvertNoTokenLimitOmitsMaxOutputTokens(t *testing.T) {
	req := &model.InternalLLMRequest{Model: "gemini-3.1-pro-preview"}

	got := convertLLMToGeminiRequest(req)
	if got.GenerationConfig != nil && got.GenerationConfig.MaxOutputTokens != 0 {
		t.Fatalf("generationConfig = %+v, want 无 maxOutputTokens", got.GenerationConfig)
	}
}
