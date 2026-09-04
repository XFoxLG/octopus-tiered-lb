package price

import (
	"strings"
	"time"

	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/op/llm"
)

const (
	// BillingWindowNone 表示模型不套峰谷计费（未命中任何启用规则）。
	BillingWindowNone = ""
	// BillingWindowPeak 高峰：命中规则的窗口内（北京时间）。
	BillingWindowPeak = "peak"
	// BillingWindowOffPeak 空闲：命中规则但不在任何窗口内。
	BillingWindowOffPeak = "offpeak"
)

// deepSeekLoc 固定为北京时区（Asia/Shanghai 加载失败时回退 UTC+8）。
// 峰谷窗口判定与时区、统计时区（stats_timezone）及容器 TZ 完全解耦。
var deepSeekLoc = loadDeepSeekLocation()

func loadDeepSeekLocation() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.FixedZone("UTC+8", 8*3600)
}

func deepSeekLocation() *time.Location {
	return deepSeekLoc
}

// inWindow 判断 minutes（自 00:00 起）是否在半开区间 [start, end) 内。
// start == end（或 start > end）的窗口视为无效，恒为 false。
func inWindow(minutes, start, end int) bool {
	if start >= end || end > 24*60 {
		return false
	}
	return minutes >= start && minutes < end
}

// BillingWindow 返回模型 modelName 在时刻 at 所属的计费窗口。
// 规则驱动：命中启用的峰谷规则（llm.PriceScheduleMatch）按规则的北京窗口
// 判定高峰/空闲；未命中返回 ""。窗口分钟按北京时间换算（模型名大小写不敏感）。
// 规则开启 WeekendOffPeak 时，北京时间周六/周日全天按空闲计费（官方
// 2026-08-23 起 DeepSeek 周末不再区分峰谷，统一低谷价）。
func BillingWindow(modelName string, at time.Time) string {
	sched := llm.PriceScheduleMatch(strings.ToLower(modelName))
	if sched == nil {
		return BillingWindowNone
	}
	local := at.In(deepSeekLocation())
	if sched.WeekendOffPeak {
		if wd := local.Weekday(); wd == time.Saturday || wd == time.Sunday {
			return BillingWindowOffPeak
		}
	}
	mins := local.Hour()*60 + local.Minute()
	if inWindow(mins, sched.Window1Start, sched.Window1End) ||
		inWindow(mins, sched.Window2Start, sched.Window2End) {
		return BillingWindowPeak
	}
	return BillingWindowOffPeak
}

// ScaleLLMPrice 将价格的四个字段统一乘以 mul。
func ScaleLLMPrice(p model.LLMPrice, mul float64) model.LLMPrice {
	return model.LLMPrice{
		Input:      p.Input * mul,
		Output:     p.Output * mul,
		CacheRead:  p.CacheRead * mul,
		CacheWrite: p.CacheWrite * mul,
	}
}

// EffectiveLLMPrice 返回模型 modelName 在时刻 at 的有效计费价。
// 规则驱动：
//   - 命中启用规则 → 高峰窗口返回规则高峰价；空闲窗口返回高峰价 × off_peak_mul。
//   - 未命中规则 → GetLLMPrice 原样返回（不缩放），与旧版非 DeepSeek 行为一致。
func EffectiveLLMPrice(modelName string, at time.Time) *model.LLMPrice {
	sched := llm.PriceScheduleMatch(strings.ToLower(modelName))
	if sched != nil {
		peak := sched.LLMPrice
		if BillingWindow(modelName, at) == BillingWindowOffPeak {
			scaled := ScaleLLMPrice(peak, sched.OffPeakMul)
			return &scaled
		}
		return &peak
	}
	p := GetLLMPrice(modelName)
	if p == nil {
		return nil
	}
	return p
}
