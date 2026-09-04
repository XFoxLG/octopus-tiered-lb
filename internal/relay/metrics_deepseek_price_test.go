package relay

import (
	"testing"
	"time"

	transmodel "github.com/lingyuins/octopus/internal/transformer/model"
)

// shanghaiLocRelay 与 price 包 deepSeekLocation 相同固定偏移，用于构造北京时刻。
var shanghaiLocRelay = time.FixedZone("UTC+8", 8*3600)

func beijingRelay(t *testing.T, h, m int) time.Time {
	t.Helper()
	return time.Date(2026, 8, 17, h, m, 0, 0, shanghaiLocRelay)
}

// TestSetInternalResponseDeepSeekPeakPricing 验证 DeepSeek v4 白名单模型的
// 计费随请求开始时刻（StartTime）的峰谷窗口变化：
//   - 北京 10:00（高峰）→ 目录高峰价
//   - 北京 13:00（空闲）→ 高峰价 ×0.5
//   - 非 DeepSeek 模型两个时刻费用相同
func TestSetInternalResponseDeepSeekPeakPricing(t *testing.T) {
	// 依赖 price 包 presets_manual.go 的高峰预设（GetLLMPrice 经 map 命中）。
	// 1e6 uncached input + 1e6 output，便于直接对比价格数值。
	resp := &transmodel.InternalLLMResponse{
		Usage: &transmodel.Usage{
			PromptTokens:     2_000_000, // 其中 1e6 cached、1e6 uncached
			CompletionTokens: 1_000_000,
			PromptTokensDetails: &transmodel.PromptTokensDetails{
				CachedTokens: 1_000_000,
			},
		},
	}

	peak := NewRelayMetrics(1, "deepseek-v4-flash", "chat", "chat", "127.0.0.1", nil)
	peak.StartTime = beijingRelay(t, 10, 0)
	peak.SetInternalResponse(resp, "deepseek-v4-flash")

	off := NewRelayMetrics(1, "deepseek-v4-flash", "chat", "chat", "127.0.0.1", nil)
	off.StartTime = beijingRelay(t, 13, 0)
	off.SetInternalResponse(resp, "deepseek-v4-flash")

	if peak.Stats.InputCost <= 0 {
		t.Fatalf("peak InputCost = %v, want > 0", peak.Stats.InputCost)
	}
	// 空闲 = 高峰 × 0.5（off-peak 倍率）
	if !floatNear(off.Stats.InputCost, peak.Stats.InputCost*0.5) {
		t.Fatalf("offpeak InputCost = %v, want peak*0.5 = %v", off.Stats.InputCost, peak.Stats.InputCost*0.5)
	}
	if !floatNear(off.Stats.OutputCost, peak.Stats.OutputCost*0.5) {
		t.Fatalf("offpeak OutputCost = %v, want peak*0.5 = %v", off.Stats.OutputCost, peak.Stats.OutputCost*0.5)
	}

	// 非 DeepSeek：两个时刻费用相同。
	gptResp := &transmodel.InternalLLMResponse{
		Usage: &transmodel.Usage{
			PromptTokens:     1_000_000,
			CompletionTokens: 1_000_000,
			PromptTokensDetails: &transmodel.PromptTokensDetails{
				CachedTokens: 0,
			},
		},
	}
	gptPeak := NewRelayMetrics(1, "gpt-4o", "chat", "chat", "127.0.0.1", nil)
	gptPeak.StartTime = beijingRelay(t, 10, 0)
	gptPeak.SetInternalResponse(gptResp, "gpt-4o")

	gptOff := NewRelayMetrics(1, "gpt-4o", "chat", "chat", "127.0.0.1", nil)
	gptOff.StartTime = beijingRelay(t, 13, 0)
	gptOff.SetInternalResponse(gptResp, "gpt-4o")

	if !floatNear(gptPeak.Stats.InputCost, gptOff.Stats.InputCost) {
		t.Fatalf("gpt-4o InputCost differs by window: %v vs %v", gptPeak.Stats.InputCost, gptOff.Stats.InputCost)
	}
}

// TestSetInternalResponseUnknownModel 验证未知模型（无价格）时 SetInternalResponse
// 不 panic、不产生费用（与原有 GetLLMPrice nil 行为一致）。
func TestSetInternalResponseUnknownModel(t *testing.T) {
	resp := &transmodel.InternalLLMResponse{
		Usage: &transmodel.Usage{
			PromptTokens:     100,
			CompletionTokens: 100,
			PromptTokensDetails: &transmodel.PromptTokensDetails{
				CachedTokens: 0,
			},
		},
	}
	m := NewRelayMetrics(1, "totally-unknown-model", "chat", "chat", "127.0.0.1", nil)
	m.StartTime = beijingRelay(t, 10, 0)
	m.SetInternalResponse(resp, "totally-unknown-model")
	if m.Stats.InputCost != 0 || m.Stats.OutputCost != 0 {
		t.Fatalf("unknown model costs = %v/%v, want 0/0", m.Stats.InputCost, m.Stats.OutputCost)
	}
}

func floatNear(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-9
}
